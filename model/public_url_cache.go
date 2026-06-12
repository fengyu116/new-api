package model

type PublicURLCache struct {
	Id         int    `json:"id" gorm:"primaryKey"`
	UserId     int    `json:"user_id" gorm:"index"`
	Sha256     string `json:"sha256" gorm:"type:varchar(64);not null;uniqueIndex:idx_public_url_cache_provider_bucket_sha"`
	Provider   string `json:"provider" gorm:"type:varchar(32);not null;uniqueIndex:idx_public_url_cache_provider_bucket_sha"`
	Bucket     string `json:"bucket" gorm:"type:varchar(512);not null;uniqueIndex:idx_public_url_cache_provider_bucket_sha"`
	ObjectKey  string `json:"object_key" gorm:"type:varchar(512);not null"`
	MimeType   string `json:"mime_type" gorm:"type:varchar(128);not null"`
	Size       int64  `json:"size" gorm:"not null;default:0"`
	RefCount   int    `json:"ref_count" gorm:"not null;default:0"`
	ExpiresAt  int64  `json:"expires_at" gorm:"index;not null"`
	CreatedAt  int64  `json:"created_at" gorm:"index;not null"`
	LastUsedAt int64  `json:"last_used_at" gorm:"not null"`
	DeletedAt  int64  `json:"deleted_at" gorm:"index;not null;default:0"`
}

type PublicURLCacheUsage struct {
	Id         int    `json:"id" gorm:"primaryKey"`
	CacheId    int    `json:"cache_id" gorm:"not null;index;uniqueIndex:idx_public_url_cache_usage_once"`
	UserId     int    `json:"user_id" gorm:"not null;index"`
	UsageId    string `json:"usage_id" gorm:"type:varchar(128);not null;index;uniqueIndex:idx_public_url_cache_usage_once"`
	Purpose    string `json:"purpose" gorm:"type:varchar(128);not null"`
	CreatedAt  int64  `json:"created_at" gorm:"not null"`
	ReleasedAt int64  `json:"released_at" gorm:"index;not null;default:0"`
}
