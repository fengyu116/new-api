package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	PublicURLCacheProviderLocal = "local"

	defaultPublicURLCacheTTLSeconds = int64(3600)
	defaultPublicURLCacheMaxImageMB = int64(10)
	defaultPublicURLCachePrefix     = "toonflow-refs"
	defaultPublicURLCacheLocalPath  = "data/public-url-cache"
)

type PublicURLCacheConfig struct {
	Enabled       bool
	Provider      string
	Prefix        string
	TTLSeconds    int64
	MaxImageMB    int64
	LocalPath     string
	PublicBaseURL string
}

type PublicURLCacheCreateRequest struct {
	Images     []string `json:"images"`
	Purpose    string   `json:"purpose"`
	TTLSeconds int64    `json:"ttl_seconds"`
	UsageID    string   `json:"usage_id"`
}

type PublicURLCacheReleaseRequest struct {
	UsageID string `json:"usage_id"`
}

type PublicURLCacheItem struct {
	URL       string `json:"url"`
	Sha256    string `json:"sha256"`
	ObjectKey string `json:"object_key"`
	Cached    bool   `json:"cached"`
	ExpiresAt int64  `json:"expires_at"`
}

type PublicURLCacheCreateResponse struct {
	UsageID string               `json:"usage_id"`
	Data    []PublicURLCacheItem `json:"data"`
}

type publicURLObjectStore interface {
	PutObject(objectKey string, data []byte, contentType string) error
	DeleteObject(objectKey string) error
	ObjectURL(objectKey string, ttlSeconds int64) (string, error)
	ObjectPath(objectKey string) (string, error)
}

type localPublicURLStore struct {
	cfg PublicURLCacheConfig
}

func newLocalPublicURLStore(cfg PublicURLCacheConfig) (publicURLObjectStore, error) {
	if err := os.MkdirAll(cfg.LocalPath, 0755); err != nil {
		return nil, fmt.Errorf("创建本地公网缓存目录失败: %w", err)
	}
	return &localPublicURLStore{cfg: cfg}, nil
}

func (s *localPublicURLStore) PutObject(objectKey string, data []byte, contentType string) error {
	objectPath, err := s.ObjectPath(objectKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(objectPath), 0755); err != nil {
		return fmt.Errorf("创建本地公网缓存子目录失败: %w", err)
	}
	if err := os.WriteFile(objectPath, data, 0644); err != nil {
		return fmt.Errorf("写入本地公网缓存文件失败: %w", err)
	}
	return nil
}

func (s *localPublicURLStore) DeleteObject(objectKey string) error {
	objectPath, err := s.ObjectPath(objectKey)
	if err != nil {
		return err
	}
	if err := os.Remove(objectPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除本地公网缓存文件失败: %w", err)
	}
	return nil
}

func (s *localPublicURLStore) ObjectURL(objectKey string, ttlSeconds int64) (string, error) {
	baseURL := strings.TrimRight(s.cfg.PublicBaseURL, "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(system_setting.ServerAddress, "/")
	}
	if baseURL == "" {
		return "", errors.New("请配置 PublicUrlCachePublicBaseURL 或 ServerAddress，才能生成公网 URL")
	}
	return baseURL + "/public-url-cache/" + escapeObjectKeyPath(objectKey), nil
}

func (s *localPublicURLStore) ObjectPath(objectKey string) (string, error) {
	cleanObjectKey := path.Clean(strings.TrimPrefix(objectKey, "/"))
	if cleanObjectKey == "." || strings.HasPrefix(cleanObjectKey, "../") || cleanObjectKey == ".." {
		return "", errors.New("非法的缓存对象路径")
	}
	root, err := filepath.Abs(s.cfg.LocalPath)
	if err != nil {
		return "", err
	}
	fullPath := filepath.Join(root, filepath.FromSlash(cleanObjectKey))
	rel, err := filepath.Rel(root, fullPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("非法的缓存对象路径")
	}
	return fullPath, nil
}

var newPublicURLObjectStore = newLocalPublicURLStore

func GetPublicURLCacheConfig() PublicURLCacheConfig {
	return PublicURLCacheConfig{
		Enabled:       parseOptionBool("PublicUrlCacheEnabled", false),
		Provider:      getOptionString("PublicUrlCacheProvider", PublicURLCacheProviderLocal),
		Prefix:        strings.Trim(strings.TrimSpace(getOptionString("PublicUrlCachePrefix", defaultPublicURLCachePrefix)), "/"),
		TTLSeconds:    parseOptionInt64("PublicUrlCacheTTLSeconds", defaultPublicURLCacheTTLSeconds),
		MaxImageMB:    parseOptionInt64("PublicUrlCacheMaxImageMB", defaultPublicURLCacheMaxImageMB),
		LocalPath:     strings.TrimSpace(getOptionString("PublicUrlCacheLocalPath", defaultPublicURLCacheLocalPath)),
		PublicBaseURL: strings.TrimSpace(getOptionString("PublicUrlCachePublicBaseURL", "")),
	}
}

func CreatePublicURLCache(userID int, req PublicURLCacheCreateRequest) (*PublicURLCacheCreateResponse, error) {
	cfg := GetPublicURLCacheConfig()
	if err := validatePublicURLCacheConfig(cfg); err != nil {
		return nil, err
	}
	if len(req.Images) == 0 {
		return nil, errors.New("images 不能为空")
	}
	if req.Purpose == "" {
		req.Purpose = "toonflow-reference"
	}
	ttlSeconds := req.TTLSeconds
	if ttlSeconds <= 0 {
		ttlSeconds = cfg.TTLSeconds
	}
	if ttlSeconds <= 0 {
		ttlSeconds = defaultPublicURLCacheTTLSeconds
	}
	usageID := strings.TrimSpace(req.UsageID)
	if usageID == "" {
		usageID = "toonflow-task-" + uuid.NewString()
	}

	store, err := newPublicURLObjectStore(cfg)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	expiresAt := now + ttlSeconds
	items := make([]PublicURLCacheItem, 0, len(req.Images))
	for index, rawImage := range req.Images {
		rawImage = strings.TrimSpace(rawImage)
		if rawImage == "" {
			return nil, fmt.Errorf("第 %d 张图片为空", index+1)
		}
		if isHTTPURL(rawImage) {
			items = append(items, PublicURLCacheItem{URL: rawImage, Cached: false, ExpiresAt: expiresAt})
			continue
		}
		item, err := cacheBase64Image(userID, usageID, req.Purpose, rawImage, cfg, store, expiresAt, now)
		if err != nil {
			return nil, fmt.Errorf("第 %d 张图片处理失败: %w", index+1, err)
		}
		items = append(items, item)
	}
	return &PublicURLCacheCreateResponse{UsageID: usageID, Data: items}, nil
}

func ReleasePublicURLCache(userID int, usageID string) (int, error) {
	usageID = strings.TrimSpace(usageID)
	if usageID == "" {
		return 0, errors.New("usage_id 不能为空")
	}
	cfg := GetPublicURLCacheConfig()
	if cfg.Provider == "" {
		cfg.Provider = PublicURLCacheProviderLocal
	}
	store, err := newPublicURLObjectStore(cfg)
	if err != nil {
		return 0, err
	}
	return releasePublicURLCacheUsage(userID, usageID, store)
}

func ResolvePublicURLCacheObjectPath(objectKey string) (string, string, error) {
	cfg := GetPublicURLCacheConfig()
	if err := validatePublicURLCacheConfig(cfg); err != nil {
		return "", "", err
	}
	store, err := newPublicURLObjectStore(cfg)
	if err != nil {
		return "", "", err
	}
	objectPath, err := store.ObjectPath(objectKey)
	if err != nil {
		return "", "", err
	}
	var cache model.PublicURLCache
	err = model.DB.Where("provider = ? AND bucket = ? AND object_key = ? AND deleted_at = 0 AND expires_at >= ?",
		cfg.Provider, cfg.LocalPath, path.Clean(strings.TrimPrefix(objectKey, "/")), time.Now().Unix()).
		First(&cache).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", errors.New("缓存文件不存在或已过期")
	}
	if err != nil {
		return "", "", err
	}
	return objectPath, cache.MimeType, nil
}

func StartPublicURLCacheCleanupTask() {
	if !common.IsMasterNode {
		return
	}
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			<-ticker.C
			if !GetPublicURLCacheConfig().Enabled {
				continue
			}
			if err := CleanupExpiredPublicURLCaches(); err != nil {
				common.SysLog("public url cache cleanup failed: " + err.Error())
			}
		}
	}()
}

func CleanupExpiredPublicURLCaches() error {
	cfg := GetPublicURLCacheConfig()
	if err := validatePublicURLCacheConfig(cfg); err != nil {
		return nil
	}
	store, err := newPublicURLObjectStore(cfg)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	var caches []model.PublicURLCache
	if err := model.DB.Where("deleted_at = 0 AND expires_at < ?", now).Limit(200).Find(&caches).Error; err != nil {
		return err
	}
	for _, cache := range caches {
		if err := deletePublicURLCacheObject(&cache, store, now); err != nil {
			common.SysLog(fmt.Sprintf("delete expired public url cache %d failed: %s", cache.Id, err.Error()))
		}
	}
	return nil
}

func cacheBase64Image(userID int, usageID string, purpose string, rawImage string, cfg PublicURLCacheConfig, store publicURLObjectStore, expiresAt int64, now int64) (PublicURLCacheItem, error) {
	mimeType, cleanBase64, err := DecodeBase64FileData(rawImage)
	if err != nil {
		return PublicURLCacheItem{}, err
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return PublicURLCacheItem{}, fmt.Errorf("仅支持 image/*，当前 MIME: %s", mimeType)
	}
	data, err := base64.StdEncoding.DecodeString(cleanBase64)
	if err != nil {
		return PublicURLCacheItem{}, fmt.Errorf("base64 解码失败: %w", err)
	}
	if len(data) == 0 {
		return PublicURLCacheItem{}, errors.New("图片内容为空")
	}
	if int64(len(data)) > cfg.MaxImageMB*1024*1024 {
		return PublicURLCacheItem{}, fmt.Errorf("图片大小超过限制 %dMB", cfg.MaxImageMB)
	}

	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	objectKey := buildPublicURLCacheObjectKey(cfg.Prefix, sha, mimeType, now)
	cached := true
	cache, err := findPublicURLCache(cfg.Provider, cfg.LocalPath, sha)
	if err != nil {
		return PublicURLCacheItem{}, err
	}
	if cache == nil || cache.DeletedAt > 0 || cache.ObjectKey == "" {
		cached = false
		if err := store.PutObject(objectKey, data, mimeType); err != nil {
			return PublicURLCacheItem{}, err
		}
		cache, err = upsertPublicURLCache(userID, sha, cfg, objectKey, mimeType, int64(len(data)), expiresAt, now)
		if err != nil {
			return PublicURLCacheItem{}, err
		}
	} else {
		objectKey = cache.ObjectKey
		if expiresAt > cache.ExpiresAt {
			if err := model.DB.Model(cache).Updates(map[string]any{
				"expires_at":   expiresAt,
				"last_used_at": now,
			}).Error; err != nil {
				return PublicURLCacheItem{}, err
			}
		}
	}
	if err := attachPublicURLCacheUsage(cache.Id, userID, usageID, purpose, expiresAt, now); err != nil {
		return PublicURLCacheItem{}, err
	}
	publicURL, err := store.ObjectURL(objectKey, expiresAt-now)
	if err != nil {
		return PublicURLCacheItem{}, err
	}
	return PublicURLCacheItem{
		URL:       publicURL,
		Sha256:    sha,
		ObjectKey: objectKey,
		Cached:    cached,
		ExpiresAt: expiresAt,
	}, nil
}

func findPublicURLCache(provider, bucket, sha string) (*model.PublicURLCache, error) {
	var cache model.PublicURLCache
	err := model.DB.Where("provider = ? AND bucket = ? AND sha256 = ?", provider, bucket, sha).First(&cache).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cache, nil
}

func upsertPublicURLCache(userID int, sha string, cfg PublicURLCacheConfig, objectKey string, mimeType string, size int64, expiresAt int64, now int64) (*model.PublicURLCache, error) {
	cache := model.PublicURLCache{
		UserId:     userID,
		Sha256:     sha,
		Provider:   cfg.Provider,
		Bucket:     cfg.LocalPath,
		ObjectKey:  objectKey,
		MimeType:   mimeType,
		Size:       size,
		RefCount:   0,
		ExpiresAt:  expiresAt,
		CreatedAt:  now,
		LastUsedAt: now,
		DeletedAt:  0,
	}
	err := model.DB.Create(&cache).Error
	if err == nil {
		return &cache, nil
	}
	existing, findErr := findPublicURLCache(cfg.Provider, cfg.LocalPath, sha)
	if findErr != nil {
		return nil, findErr
	}
	if existing == nil {
		return nil, err
	}
	if updateErr := model.DB.Model(existing).Updates(map[string]any{
		"user_id":      userID,
		"object_key":   objectKey,
		"mime_type":    mimeType,
		"size":         size,
		"expires_at":   expiresAt,
		"last_used_at": now,
		"deleted_at":   0,
	}).Error; updateErr != nil {
		return nil, updateErr
	}
	existing.UserId = userID
	existing.ObjectKey = objectKey
	existing.MimeType = mimeType
	existing.Size = size
	existing.ExpiresAt = expiresAt
	existing.LastUsedAt = now
	existing.DeletedAt = 0
	return existing, nil
}

func attachPublicURLCacheUsage(cacheID int, userID int, usageID string, purpose string, expiresAt int64, now int64) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		usage := model.PublicURLCacheUsage{
			CacheId:   cacheID,
			UserId:    userID,
			UsageId:   usageID,
			Purpose:   purpose,
			CreatedAt: now,
		}
		err := tx.Create(&usage).Error
		if err != nil {
			var existing model.PublicURLCacheUsage
			findErr := tx.Where("cache_id = ? AND usage_id = ?", cacheID, usageID).First(&existing).Error
			if findErr == nil {
				return nil
			}
			return err
		}
		return tx.Model(&model.PublicURLCache{}).Where("id = ?", cacheID).Updates(map[string]any{
			"ref_count":    gorm.Expr("ref_count + ?", 1),
			"expires_at":   expiresAt,
			"last_used_at": now,
			"deleted_at":   0,
		}).Error
	})
}

func releasePublicURLCacheUsage(userID int, usageID string, store publicURLObjectStore) (int, error) {
	now := time.Now().Unix()
	var usages []model.PublicURLCacheUsage
	if err := model.DB.Where("usage_id = ? AND user_id = ? AND released_at = 0", usageID, userID).Find(&usages).Error; err != nil {
		return 0, err
	}
	released := 0
	for _, usage := range usages {
		var shouldDelete bool
		var cache model.PublicURLCache
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.First(&cache, usage.CacheId).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.PublicURLCacheUsage{}).Where("id = ? AND released_at = 0", usage.Id).Update("released_at", now).Error; err != nil {
				return err
			}
			newRefCount := cache.RefCount - 1
			if newRefCount < 0 {
				newRefCount = 0
			}
			updates := map[string]any{"ref_count": newRefCount}
			if newRefCount == 0 && cache.DeletedAt == 0 {
				updates["deleted_at"] = now
				shouldDelete = true
			}
			return tx.Model(&model.PublicURLCache{}).Where("id = ?", cache.Id).Updates(updates).Error
		})
		if err != nil {
			return released, err
		}
		released++
		if shouldDelete {
			if err := store.DeleteObject(cache.ObjectKey); err != nil {
				return released, err
			}
		}
	}
	return released, nil
}

func deletePublicURLCacheObject(cache *model.PublicURLCache, store publicURLObjectStore, now int64) error {
	if cache == nil || cache.DeletedAt > 0 {
		return nil
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.PublicURLCacheUsage{}).Where("cache_id = ? AND released_at = 0", cache.Id).Update("released_at", now).Error; err != nil {
			return err
		}
		return tx.Model(&model.PublicURLCache{}).Where("id = ?", cache.Id).Updates(map[string]any{
			"ref_count":  0,
			"deleted_at": now,
		}).Error
	})
	if err != nil {
		return err
	}
	return store.DeleteObject(cache.ObjectKey)
}

func validatePublicURLCacheConfig(cfg PublicURLCacheConfig) error {
	if !cfg.Enabled {
		return errors.New("公网 URL 缓存未启用")
	}
	if cfg.Provider != PublicURLCacheProviderLocal {
		return fmt.Errorf("不支持的公网 URL 缓存 provider: %s", cfg.Provider)
	}
	if cfg.Prefix == "" {
		return errors.New("PublicUrlCachePrefix 不能为空")
	}
	if cfg.LocalPath == "" {
		return errors.New("PublicUrlCacheLocalPath 不能为空")
	}
	if cfg.MaxImageMB <= 0 {
		return errors.New("PublicUrlCacheMaxImageMB 必须大于 0")
	}
	return nil
}

func getOptionString(key string, fallback string) string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	if value, ok := common.OptionMap[key]; ok {
		return value
	}
	return fallback
}

func parseOptionBool(key string, fallback bool) bool {
	value := strings.TrimSpace(getOptionString(key, ""))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseOptionInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(getOptionString(key, ""))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func buildPublicURLCacheObjectKey(prefix string, sha string, mimeType string, now int64) string {
	t := time.Unix(now, 0)
	ext := mimeExtension(mimeType)
	return path.Join(prefix, t.Format("2006"), t.Format("01"), sha+ext)
}

func mimeExtension(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/heic":
		return ".heic"
	case "image/heif":
		return ".heif"
	default:
		return ".img"
	}
}

func isHTTPURL(value string) bool {
	return strings.HasPrefix(strings.ToLower(value), "http://") || strings.HasPrefix(strings.ToLower(value), "https://")
}

func escapeObjectKeyPath(objectKey string) string {
	parts := strings.Split(objectKey, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
