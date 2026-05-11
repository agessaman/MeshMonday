package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv               string
	HTTPAddr             string
	MeshName             string
	DiceBearStyle        string
	UIPollSeconds        int
	IATADefault          string
	IATAFilters          []string
	TrackFromDate        time.Time
	TZ                   string
	SQLitePath           string
	MQTTBrokerURL        string
	MQTTTopicTemplate    string
	MQTTClientID         string
	MQTTUsername         string
	MQTTPassword         string
	MQTTMaxPayloadBytes  int
	IngestMaxPacketHex   int
	IngestMaxObserver    int
	HashtagChannels      []string
	PrivateChannelKeys   []string
	ReplayDelay          time.Duration
	EnableDevSeed        bool
	RawMondayRetainWeeks int
	RetentionInterval    time.Duration
}

const dateOnlyFormat = "2006-01-02"
const publicChannelKeyHex = "8b3387e9c5cdea6ac9e5edbaa115cd72"

var diceBearStyleSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func Load() (Config, error) {
	tz := getEnv("TZ", "America/Los_Angeles")
	loc, locErr := time.LoadLocation(tz)
	if locErr != nil {
		loc = time.UTC
	}

	trackFromRaw := getEnv("TRACK_FROM_DATE", "2026-01-05")
	trackFrom, err := time.ParseInLocation(dateOnlyFormat, trackFromRaw, loc)
	if err != nil {
		return Config{}, fmt.Errorf("parse TRACK_FROM_DATE: %w", err)
	}

	replayDelayMs, err := strconv.Atoi(getEnv("REPLAY_DELAY_MS", "25"))
	if err != nil {
		return Config{}, fmt.Errorf("parse REPLAY_DELAY_MS: %w", err)
	}
	uiPollSeconds, err := strconv.Atoi(getEnv("UI_POLL_SECONDS", "15"))
	if err != nil {
		return Config{}, fmt.Errorf("parse UI_POLL_SECONDS: %w", err)
	}
	mqttMaxPayloadBytes, err := strconv.Atoi(getEnv("MQTT_MAX_PAYLOAD_BYTES", "16384"))
	if err != nil {
		return Config{}, fmt.Errorf("parse MQTT_MAX_PAYLOAD_BYTES: %w", err)
	}
	ingestMaxPacketHex, err := strconv.Atoi(getEnv("INGEST_MAX_PACKET_HEX_CHARS", "8192"))
	if err != nil {
		return Config{}, fmt.Errorf("parse INGEST_MAX_PACKET_HEX_CHARS: %w", err)
	}
	ingestMaxObserver, err := strconv.Atoi(getEnv("INGEST_MAX_OBSERVER_KEY_CHARS", "64"))
	if err != nil {
		return Config{}, fmt.Errorf("parse INGEST_MAX_OBSERVER_KEY_CHARS: %w", err)
	}
	rawMondayRetainWeeks, err := strconv.Atoi(getEnv("RAW_MONDAY_RETAIN_WEEKS", "12"))
	if err != nil {
		return Config{}, fmt.Errorf("parse RAW_MONDAY_RETAIN_WEEKS: %w", err)
	}
	retentionIntervalMin, err := strconv.Atoi(getEnv("RETENTION_INTERVAL_MINUTES", "60"))
	if err != nil {
		return Config{}, fmt.Errorf("parse RETENTION_INTERVAL_MINUTES: %w", err)
	}

	cfg := Config{
		AppEnv:               getEnv("APP_ENV", "development"),
		HTTPAddr:             getEnv("HTTP_ADDR", "127.0.0.1:8080"),
		MeshName:             getEnv("MESH_NAME", "MeshCore"),
		DiceBearStyle:        strings.ToLower(strings.TrimSpace(getEnv("DICEBEAR_STYLE", "adventurer"))),
		UIPollSeconds:        uiPollSeconds,
		IATADefault:          strings.ToUpper(getEnv("IATA_DEFAULT", "SEA")),
		IATAFilters:          parseIATAFilters(getEnv("IATA_FILTERS", ""), strings.ToUpper(getEnv("IATA_DEFAULT", "SEA"))),
		TrackFromDate:        trackFrom,
		TZ:                   tz,
		SQLitePath:           getEnv("SQLITE_PATH", "./data/meshmonday_dev.db"),
		MQTTBrokerURL:        getEnv("MQTT_BROKER_URL", "tcp://localhost:1883"),
		MQTTTopicTemplate:    getEnv("MQTT_TOPIC_TEMPLATE", "meshcore/+/+/packets"),
		MQTTClientID:         getEnv("MQTT_CLIENT_ID", "meshmonday-dev"),
		MQTTUsername:         os.Getenv("MQTT_USERNAME"),
		MQTTPassword:         os.Getenv("MQTT_PASSWORD"),
		MQTTMaxPayloadBytes:  mqttMaxPayloadBytes,
		IngestMaxPacketHex:   ingestMaxPacketHex,
		IngestMaxObserver:    ingestMaxObserver,
		HashtagChannels:      parseCSV(getEnv("HASHTAG_CHANNELS", "")),
		PrivateChannelKeys:   parseCSV(getEnv("PRIVATE_CHANNEL_KEYS", "")),
		ReplayDelay:          time.Duration(replayDelayMs) * time.Millisecond,
		EnableDevSeed:        parseBool(getEnv("ENABLE_DEV_SEED", "false")),
		RawMondayRetainWeeks: rawMondayRetainWeeks,
		RetentionInterval:    time.Duration(retentionIntervalMin) * time.Minute,
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.AppEnv == "production" {
		if c.MQTTUsername == "" || c.MQTTPassword == "" {
			return errors.New("production requires MQTT_USERNAME and MQTT_PASSWORD")
		}
	}
	if strings.TrimSpace(c.MeshName) == "" {
		return errors.New("MESH_NAME cannot be empty")
	}
	if !diceBearStyleSlugPattern.MatchString(c.DiceBearStyle) {
		return fmt.Errorf("invalid DICEBEAR_STYLE %q", c.DiceBearStyle)
	}
	if c.UIPollSeconds < 0 {
		return errors.New("UI_POLL_SECONDS cannot be negative")
	}
	if strings.TrimSpace(c.MQTTTopicTemplate) == "" {
		return errors.New("MQTT_TOPIC_TEMPLATE cannot be empty")
	}
	if c.MQTTMaxPayloadBytes < 256 {
		return errors.New("MQTT_MAX_PAYLOAD_BYTES must be >= 256")
	}
	if c.IngestMaxPacketHex < 64 {
		return errors.New("INGEST_MAX_PACKET_HEX_CHARS must be >= 64")
	}
	if c.IngestMaxObserver < 8 {
		return errors.New("INGEST_MAX_OBSERVER_KEY_CHARS must be >= 8")
	}
	if c.RawMondayRetainWeeks < 0 {
		return errors.New("RAW_MONDAY_RETAIN_WEEKS cannot be negative")
	}
	if c.RawMondayRetainWeeks > 1040 {
		return errors.New("RAW_MONDAY_RETAIN_WEEKS must be <= 1040")
	}
	if c.RetentionInterval < time.Minute {
		return errors.New("RETENTION_INTERVAL_MINUTES must be >= 1")
	}
	for _, iata := range c.IATAFilters {
		if len(iata) < 3 || len(iata) > 4 {
			return fmt.Errorf("invalid IATA filter %q", iata)
		}
	}
	for _, raw := range c.PrivateChannelKeys {
		value := strings.ToLower(strings.TrimSpace(raw))
		if len(value) != 32 {
			return fmt.Errorf("invalid PRIVATE_CHANNEL_KEYS entry length for %q", raw)
		}
		if _, err := hex.DecodeString(value); err != nil {
			return fmt.Errorf("invalid PRIVATE_CHANNEL_KEYS entry %q: %w", raw, err)
		}
	}
	return nil
}

func (c Config) TopicForIATA(iata string) string {
	if !strings.Contains(c.MQTTTopicTemplate, "{IATA}") {
		return c.MQTTTopicTemplate
	}
	value := strings.ToUpper(strings.TrimSpace(iata))
	if value == "" {
		value = c.IATADefault
	}
	return strings.ReplaceAll(c.MQTTTopicTemplate, "{IATA}", value)
}

func (c Config) ChannelSecretKeys() []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(key string) {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "" {
			return
		}
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	// Fixed public channel key
	add(publicChannelKeyHex)
	// Deterministic hashtag channels
	for _, channelName := range c.HashtagChannels {
		add(deriveHashtagKeyHex(channelName))
	}
	// Private non-deterministic channels from env
	for _, key := range c.PrivateChannelKeys {
		add(key)
	}
	return out
}

func (c Config) IATAFilterLabel() string {
	if len(c.IATAFilters) == 0 {
		return "ALL"
	}
	return strings.Join(c.IATAFilters, ", ")
}

func getEnv(key, defaultValue string) string {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	return v
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func parseIATAFilters(raw, fallback string) []string {
	value := strings.TrimSpace(raw)
	if value != "" && isAllIATAValue(value) {
		return nil
	}
	if value == "" {
		if isAllIATAValue(fallback) {
			return nil
		}
		value = fallback
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		iata := strings.ToUpper(strings.TrimSpace(part))
		if iata == "" {
			continue
		}
		if isAllIATAValue(iata) {
			return nil
		}
		if _, ok := seen[iata]; ok {
			continue
		}
		seen[iata] = struct{}{}
		out = append(out, iata)
	}
	return out
}

func isAllIATAValue(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", "ALL", "*", "ANY":
		return true
	default:
		return false
	}
}

func deriveHashtagKeyHex(channelName string) string {
	name := strings.TrimSpace(channelName)
	if name == "" {
		return ""
	}
	if !strings.HasPrefix(name, "#") {
		name = "#" + name
	}
	name = strings.ToLower(name)
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:16])
}
