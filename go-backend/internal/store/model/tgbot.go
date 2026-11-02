package model

// Plan 196 「Telegram 通知」: the panel's own Telegram bot reminds the bound
// admin chats before a machine's (rolling) due date and records 「已续费」.
// The bot token and its HMAC key live in vite_config (tg_bot_* keys, never
// returned by any API); these tables only hold chats and reminder state.

// TGBotChat is one bound admin chat (private chat or group), at most 5.
type TGBotChat struct {
	ID     int64  `gorm:"primaryKey;autoIncrement" json:"-"`
	ChatID int64  `gorm:"column:chat_id;not null;uniqueIndex:idx_tg_bot_chat_chat" json:"chatId"`
	Kind   string `gorm:"column:kind;type:varchar(16);not null;default:''" json:"kind"`
	Title  string `gorm:"column:title;type:varchar(128);not null;default:''" json:"title"`
	// LastError: the last permanent send failure (bot blocked, chat gone),
	// cleared by the next successful send.
	LastError   string `gorm:"column:last_error;type:varchar(200);not null;default:''" json:"lastError"`
	LastErrorAt int64  `gorm:"column:last_error_at;not null;default:0" json:"lastErrorAt"`
	CreatedTime int64  `gorm:"column:created_time;not null;default:0" json:"createdTime"`
}

func (TGBotChat) TableName() string { return "tg_bot_chat" }

// TGBotSent is one reminder message sent to one chat. The unique key
// (machine, due date, days before, chat) makes every reminder go out at most
// once, also across restarts; the message id lets 「已续费」 edit every copy.
type TGBotSent struct {
	ID         int64  `gorm:"primaryKey;autoIncrement" json:"-"`
	MachineKey string `gorm:"column:machine_key;type:varchar(24);not null;uniqueIndex:idx_tg_bot_sent_key,priority:1" json:"machineKey"`
	DueMs      int64  `gorm:"column:due_ms;not null;uniqueIndex:idx_tg_bot_sent_key,priority:2" json:"dueMs"`
	Days       int    `gorm:"column:days;not null;uniqueIndex:idx_tg_bot_sent_key,priority:3" json:"days"`
	ChatID     int64  `gorm:"column:chat_id;not null;uniqueIndex:idx_tg_bot_sent_key,priority:4" json:"chatId"`
	MessageID  int64  `gorm:"column:message_id;not null;default:0" json:"messageId"`
	Text       string `gorm:"column:text;type:text;not null" json:"text"`
	SentTime   int64  `gorm:"column:sent_time;not null;default:0" json:"sentTime"`
}

func (TGBotSent) TableName() string { return "tg_bot_sent" }

// TGBotAck is 「已续费」 for one machine and one due date: no more reminders
// for that due date. The next cycle has another due date, so nothing needs to
// be reset when the cycle rolls.
type TGBotAck struct {
	ID         int64  `gorm:"primaryKey;autoIncrement" json:"-"`
	MachineKey string `gorm:"column:machine_key;type:varchar(24);not null;uniqueIndex:idx_tg_bot_ack_key,priority:1" json:"machineKey"`
	DueMs      int64  `gorm:"column:due_ms;not null;uniqueIndex:idx_tg_bot_ack_key,priority:2" json:"dueMs"`
	ChatID     int64  `gorm:"column:chat_id;not null;default:0" json:"chatId"`
	ByName     string `gorm:"column:by_name;type:varchar(64);not null;default:''" json:"byName"`
	ActedTime  int64  `gorm:"column:acted_time;not null;default:0" json:"actedTime"`
}

func (TGBotAck) TableName() string { return "tg_bot_ack" }
