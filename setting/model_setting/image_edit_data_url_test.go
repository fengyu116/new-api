package model_setting

import "testing"

func TestIsImageEditDataURLConversionAllowed(t *testing.T) {
	settings := GetGlobalSettings()
	originalWhitelist := append([]int(nil), settings.ImageEditDataURLUserWhitelist...)
	originalBlacklist := append([]int(nil), settings.ImageEditDataURLUserBlacklist...)
	t.Cleanup(func() {
		settings.ImageEditDataURLUserWhitelist = originalWhitelist
		settings.ImageEditDataURLUserBlacklist = originalBlacklist
	})

	tests := []struct {
		name      string
		whitelist []int
		blacklist []int
		userID    int
		allowed   bool
	}{
		{name: "empty lists allow all authenticated users", userID: 7, allowed: true},
		{name: "whitelist limits conversion", whitelist: []int{7}, userID: 8, allowed: false},
		{name: "whitelist allows listed user", whitelist: []int{7}, userID: 7, allowed: true},
		{name: "blacklist overrides whitelist", whitelist: []int{7}, blacklist: []int{7}, userID: 7, allowed: false},
		{name: "invalid user is never allowed", userID: 0, allowed: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings.ImageEditDataURLUserWhitelist = test.whitelist
			settings.ImageEditDataURLUserBlacklist = test.blacklist
			if actual := IsImageEditDataURLConversionAllowed(test.userID); actual != test.allowed {
				t.Fatalf("IsImageEditDataURLConversionAllowed(%d) = %v, want %v", test.userID, actual, test.allowed)
			}
		})
	}
}
