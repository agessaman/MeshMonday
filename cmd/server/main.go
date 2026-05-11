package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"meshmonday/internal/config"
	"meshmonday/internal/ingest"
	"meshmonday/internal/logging"
	"meshmonday/internal/mqtt"
	"meshmonday/internal/storage"
	"meshmonday/internal/web"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 20 * time.Second
	idleTimeout       = 60 * time.Second
	maxHeaderBytes    = 1 << 20
)

func main() {
	_ = config.LoadEnvFile(".env.local")
	logger := logging.NewJSONLogger()

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err.Error())
		os.Exit(1)
	}

	store, err := storage.OpenSQLite(cfg.SQLitePath)
	if err != nil {
		logger.Error("open sqlite", "error", err.Error())
		os.Exit(1)
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Warn("close sqlite", "error", err.Error())
		}
	}()

	webServer, err := web.NewServer(cfg, store, logger)
	if err != nil {
		logger.Error("create web server", "error", err.Error())
		os.Exit(1)
	}

	httpServer := newHTTPServer(cfg.HTTPAddr, webServer.Routes())

	retentionCtx, stopRetention := context.WithCancel(context.Background())
	defer stopRetention()
	go runRetentionLoop(retentionCtx, store, cfg, logger)

	ingestService := ingest.NewService(cfg, store, logger)
	mqttConsumer := mqtt.NewConsumer(
		logger,
		cfg.MQTTBrokerURL,
		cfg.MQTTClientID,
		cfg.MQTTUsername,
		cfg.MQTTPassword,
		cfg.MQTTMaxPayloadBytes,
	)
	topic := cfg.TopicForIATA(cfg.IATADefault)
	if err := mqttConsumer.Subscribe(topic, func(ctx context.Context, msg mqtt.Message) {
		ingestService.HandleMessage(ctx, msg.Topic, msg.PayloadHex, msg.ObservedAt)
	}); err != nil {
		logger.Warn("mqtt subscribe registration failed", "error", err.Error(), "topic", topic)
	}
	defer mqttConsumer.Close()

	go connectMQTTWithRetry(logger, mqttConsumer)

	go func() {
		logger.Info("http listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", "error", err.Error())
			os.Exit(1)
		}
	}()

	waitForShutdown(logger, httpServer, stopRetention)
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

func connectMQTTWithRetry(logger *slog.Logger, consumer *mqtt.Consumer) {
	const retryDelay = 10 * time.Second
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := consumer.Connect(ctx)
		cancel()
		if err == nil {
			return
		}
		logger.Warn("mqtt connect failed; app still serving http", "error", err.Error())
		time.Sleep(retryDelay)
	}
}

func runRetentionLoop(ctx context.Context, store *storage.SQLiteStore, cfg config.Config, logger *slog.Logger) {
	jitter := time.Duration(5+time.Now().UnixNano()%21) * time.Second
	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	ticker := time.NewTicker(cfg.RetentionInterval)
	defer ticker.Stop()

	runOnce := func() {
		n, err := store.PruneRawPackets(ctx, time.Now().UTC(), cfg.TZ, cfg.RawMondayRetainWeeks)
		if err != nil {
			logger.Error("raw packet retention prune failed", "error", err.Error())
			return
		}
		if n == 0 {
			logger.Debug("raw packet retention prune complete", "deleted", n)
			return
		}
		st, statErr := store.AfterPruneMaintenance(ctx)
		logArgs := []any{"deleted", n}
		if statErr != nil {
			logArgs = append(logArgs, "space_stats_error", statErr.Error())
		} else {
			logArgs = append(logArgs,
				"freelist_pages", st.FreelistCount,
				"freelist_mib", float64(st.FreelistBytes)/(1024*1024),
				"db_file_mib", float64(st.FileSizeBytes)/(1024*1024),
				"wal_mib", float64(st.WALSizeBytes)/(1024*1024),
			)
		}
		if n >= storage.VacuumHintThreshold() || (statErr == nil && st.FreelistBytes > 50*1024*1024) {
			logArgs = append(logArgs, "shrink_hint", "SQLite keeps freed pages in the file until VACUUM; run make vacuum during maintenance. WAL was checkpointed to trim -wal if possible.")
		}
		logger.Info("raw packet retention prune complete", logArgs...)
	}

	runOnce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

func waitForShutdown(logger *slog.Logger, httpServer *http.Server, stopRetention context.CancelFunc) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	stopRetention()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Warn("http shutdown failed", "error", err.Error())
	}
}
