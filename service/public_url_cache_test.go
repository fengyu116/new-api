package service

import (
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPublicURLCacheTest(t *testing.T) string {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.PublicURLCache{}, &model.PublicURLCacheUsage{}))
	model.DB = db

	cacheDir := t.TempDir()
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{
		"PublicUrlCacheEnabled":       "true",
		"PublicUrlCacheProvider":      PublicURLCacheProviderLocal,
		"PublicUrlCachePrefix":        "toonflow-refs",
		"PublicUrlCacheTTLSeconds":    "3600",
		"PublicUrlCacheMaxImageMB":    "10",
		"PublicUrlCacheLocalPath":     cacheDir,
		"PublicUrlCachePublicBaseURL": "https://newapi.example.com",
	}
	common.OptionMapRWMutex.Unlock()

	previousServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://server-address.example.com"
	t.Cleanup(func() {
		system_setting.ServerAddress = previousServerAddress
	})
	return cacheDir
}

func TestCreatePublicURLCacheUploadsReusesServesAndReleases(t *testing.T) {
	setupPublicURLCacheTest(t)
	image := "data:image/jpeg;base64,aGVsbG8="

	first, err := CreatePublicURLCache(7, PublicURLCacheCreateRequest{
		Images:  []string{image},
		UsageID: "task-1",
	})
	require.NoError(t, err)
	require.Len(t, first.Data, 1)
	require.False(t, first.Data[0].Cached)
	require.Equal(t, "task-1", first.UsageID)
	require.True(t, strings.HasPrefix(first.Data[0].URL, "https://newapi.example.com/public-url-cache/toonflow-refs/"))

	objectPath, mimeType, err := ResolvePublicURLCacheObjectPath(first.Data[0].ObjectKey)
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", mimeType)
	require.FileExists(t, objectPath)

	second, err := CreatePublicURLCache(7, PublicURLCacheCreateRequest{
		Images:  []string{image},
		UsageID: "task-2",
	})
	require.NoError(t, err)
	require.True(t, second.Data[0].Cached)
	require.Equal(t, first.Data[0].ObjectKey, second.Data[0].ObjectKey)

	released, err := ReleasePublicURLCache(7, "task-1")
	require.NoError(t, err)
	require.Equal(t, 1, released)
	require.FileExists(t, objectPath)

	released, err = ReleasePublicURLCache(7, "task-2")
	require.NoError(t, err)
	require.Equal(t, 1, released)
	_, statErr := os.Stat(objectPath)
	require.True(t, os.IsNotExist(statErr))
}

func TestCreatePublicURLCacheKeepsOrderAndPassesThroughHTTPURLs(t *testing.T) {
	setupPublicURLCacheTest(t)
	image := "data:image/png;base64,Zm9v"
	existingURL := "https://example.com/ref.png"

	resp, err := CreatePublicURLCache(3, PublicURLCacheCreateRequest{
		Images: []string{existingURL, image},
	})
	require.NoError(t, err)
	require.Len(t, resp.Data, 2)
	require.Equal(t, existingURL, resp.Data[0].URL)
	require.False(t, resp.Data[0].Cached)
	require.True(t, strings.HasPrefix(resp.Data[1].URL, "https://newapi.example.com/public-url-cache/toonflow-refs/"))
}

func TestCreatePublicURLCacheRequiresEnabledConfig(t *testing.T) {
	setupPublicURLCacheTest(t)
	common.OptionMapRWMutex.Lock()
	common.OptionMap["PublicUrlCacheEnabled"] = "false"
	common.OptionMapRWMutex.Unlock()

	_, err := CreatePublicURLCache(1, PublicURLCacheCreateRequest{
		Images: []string{"data:image/jpeg;base64,aGVsbG8="},
	})
	require.ErrorContains(t, err, "公网 URL 缓存未启用")
}
