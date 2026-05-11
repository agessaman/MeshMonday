package config

import (
	"testing"
	"time"
)

func TestChannelSecretKeysIncludesPublicDerivedAndPrivate(t *testing.T) {
	cfg := Config{
		HashtagChannels:      []string{"#bot", "seattle"},
		PrivateChannelKeys:   []string{"00112233445566778899aabbccddeeff"},
		RawMondayRetainWeeks: 12,
		RetentionInterval:    time.Hour,
	}
	keys := cfg.ChannelSecretKeys()

	want := map[string]bool{
		"8b3387e9c5cdea6ac9e5edbaa115cd72": true, // public fixed key
		"eb50a1bcb3e4e5d7bf69a57c9dada211": true, // #bot
		"ef627a9bbbb549347fdb76bf0cd3bc14": true, // #seattle
		"00112233445566778899aabbccddeeff": true, // private key
	}

	for _, key := range keys {
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing expected keys: %v", want)
	}
}

func TestValidateDiceBearStyle(t *testing.T) {
	cfg := Config{
		MeshName:             "CascadiaMesh",
		DiceBearStyle:        "rings",
		UIPollSeconds:        15,
		IATADefault:          "SEA",
		MQTTTopicTemplate:    "meshcore/+/+/packets",
		MQTTMaxPayloadBytes:  16384,
		IngestMaxPacketHex:   8192,
		IngestMaxObserver:    64,
		RawMondayRetainWeeks: 12,
		RetentionInterval:    time.Hour,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}

	cfg.DiceBearStyle = "not a style"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid DICEBEAR_STYLE to fail validation")
	}

	cfg.DiceBearStyle = "adventurer"
	cfg.UIPollSeconds = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative UI_POLL_SECONDS to fail validation")
	}
}

func TestParseIATAFilters(t *testing.T) {
	got := parseIATAFilters("ALL", "SEA")
	if got != nil {
		t.Fatalf("expected ALL to disable filters, got %v", got)
	}

	got = parseIATAFilters("sea,pdx,yvr", "SEA")
	want := []string{"SEA", "PDX", "YVR"}
	if len(got) != len(want) {
		t.Fatalf("unexpected filter len: got=%d want=%d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected filter at %d: got=%s want=%s", i, got[i], want[i])
		}
	}

	got = parseIATAFilters("", "SEA")
	if len(got) != 1 || got[0] != "SEA" {
		t.Fatalf("expected fallback SEA filter, got %v", got)
	}
}

func TestValidateIngestLimits(t *testing.T) {
	cfg := Config{
		MeshName:             "CascadiaMesh",
		DiceBearStyle:        "fun-emoji",
		UIPollSeconds:        15,
		IATADefault:          "SEA",
		MQTTTopicTemplate:    "meshcore/+/+/packets",
		MQTTMaxPayloadBytes:  255,
		IngestMaxPacketHex:   8192,
		IngestMaxObserver:    64,
		RawMondayRetainWeeks: 12,
		RetentionInterval:    time.Hour,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected MQTT_MAX_PAYLOAD_BYTES validation to fail")
	}

	cfg.MQTTMaxPayloadBytes = 16384
	cfg.IngestMaxPacketHex = 63
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected INGEST_MAX_PACKET_HEX_CHARS validation to fail")
	}

	cfg.IngestMaxPacketHex = 8192
	cfg.IngestMaxObserver = 7
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected INGEST_MAX_OBSERVER_KEY_CHARS validation to fail")
	}
}

func TestValidateRetentionSettings(t *testing.T) {
	cfg := Config{
		MeshName:             "CascadiaMesh",
		DiceBearStyle:        "rings",
		UIPollSeconds:        15,
		IATADefault:          "SEA",
		MQTTTopicTemplate:    "meshcore/+/+/packets",
		MQTTMaxPayloadBytes:  16384,
		IngestMaxPacketHex:   8192,
		IngestMaxObserver:    64,
		RawMondayRetainWeeks: 12,
		RetentionInterval:    time.Hour,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid: %v", err)
	}

	cfg.RawMondayRetainWeeks = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative RAW_MONDAY_RETAIN_WEEKS to fail")
	}
	cfg.RawMondayRetainWeeks = 1041
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected excessive RAW_MONDAY_RETAIN_WEEKS to fail")
	}
	cfg.RawMondayRetainWeeks = 0
	cfg.RetentionInterval = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected zero RETENTION_INTERVAL to fail")
	}
}
