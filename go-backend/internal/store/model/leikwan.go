package model

// LeiKwanAccount is private panel configuration, like node and payment secrets.
// Token must never be serialized, including in the admin account response.
type LeiKwanAccount struct {
	ID          int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string `gorm:"not null" json:"name"`
	ServiceID   int64  `gorm:"not null;uniqueIndex:idx_leikwan_account_service" json:"serviceId"`
	Token       string `gorm:"type:text;not null;uniqueIndex:idx_leikwan_account_service" json:"-"`
	UpdatedTime int64  `json:"updatedTime"`
}

func (LeiKwanAccount) TableName() string { return "leikwan_account" }

// A pipe belongs to at most one ordinary FLVX tunnel. The client listen port is
// independent of the landing's allocated tunnel service port.
type LeiKwanPipe struct {
	ID            int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountID     int64  `gorm:"not null;index" json:"accountId"`
	ForwardID     int64  `gorm:"not null;uniqueIndex" json:"forwardId"`
	TunnelID      *int64 `gorm:"uniqueIndex" json:"tunnelId"`
	LandingNodeID int64  `json:"landingNodeId"`
	LandingPort   int    `json:"landingPort"`
	ListenPort    int    `json:"listenPort"`
	RouteID       int64  `json:"routeId"`
	RouteLabel    string `json:"routeLabel"`
	Transport     string `json:"transport"`
	TargetHost    string `json:"targetHost"`
	ETag          string `json:"etag"`
	UpdatedTime   int64  `json:"updatedTime"`
}

func (LeiKwanPipe) TableName() string { return "leikwan_pipe" }

// The exact payload and key survive a panel restart or an ambiguous timeout.
type LeiKwanCreate struct {
	ID            int64  `gorm:"primaryKey;autoIncrement"`
	AccountID     int64  `gorm:"not null;uniqueIndex:idx_leikwan_create"`
	RequestID     string `gorm:"not null;uniqueIndex:idx_leikwan_create"`
	Payload       string `gorm:"type:text;not null"`
	LandingNodeID int64
	ForwardID     int64
	CreatedTime   int64
}

func (LeiKwanCreate) TableName() string { return "leikwan_create" }
