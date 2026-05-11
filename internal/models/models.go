package models

import "time"

type RawPacket struct {
	ID              int64
	PacketHash      string
	Topic           string
	IATA            string
	DevicePublicKey string
	PayloadHex      string
	PayloadType     int
	PayloadTypeName string
	RouteType       int
	RouteTypeName   string
	PathLen         int
	ObservedAt      time.Time
	ReceivedAt      time.Time
}

type Checkin struct {
	ID            int64
	PacketHash    string
	Username      string
	DisplayName   string
	Message       string
	IATA          string
	CheckinDate   time.Time
	WeekStart     time.Time
	CreatedAt     time.Time
	ObserverCount int
	ObserverNames []string
}

type LeaderboardEntry struct {
	Username        string `json:"username"`
	DisplayName     string `json:"display_name"`
	MostCheckins    int    `json:"most_checkins"`
	SeasonWeeks     int    `json:"season_weeks"`
	TrackedFrom     string `json:"tracked_from"`
	LongestStreak   int    `json:"longest_streak"`
	StreakStartDate string `json:"streak_start_date"`
}
