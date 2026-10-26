package models

import "time"

// APIKey stores only a digest, never the token. Legacy archives/WAL may still
// contain the retired global credential and require normal secret retention.
type APIKey struct {
	ID          string     `json:"id" gorm:"type:varchar(36);primaryKey"`
	Name        string     `json:"name" gorm:"type:varchar(80);not null"`
	TokenDigest string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	Scopes      []string   `json:"scopes" gorm:"serializer:json;type:text;not null"`
	IssuedBy    string     `json:"issued_by" gorm:"type:varchar(36)"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at" gorm:"index"`
	RevokedAt   *time.Time `json:"revoked_at"`
}
