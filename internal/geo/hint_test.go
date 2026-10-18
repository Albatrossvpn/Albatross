package geo

import "testing"

func TestHint(t *testing.T) {
	for name, want := range map[string]string{
		"🇩🇪 DE | @channel":                     "DE",
		"🇬🇧 London":                            "GB",
		"Russian Federation - Kazan":            "RU",
		"@V2ray_Collector":                      "",
		"Germany-1":                             "DE",
		"🆔vipv2rey 🔒 HY-UDP-N/A 🇭🇰 HK down":       "HK",
		"canfingV2rayNG-58":                     "",
		"NL-Amsterdam":                          "NL",
		"کانفیگ آلمان":                          "DE",
		"Irancell":                              "",
		"vless-US-reality":                      "US",
		"Telegram : @spikevpn":                  "",
		"🇺🇸 USA-6853":                           "US",
	} {
		if got := Hint(name); got != want {
			t.Errorf("Hint(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFlag(t *testing.T) {
	if Flag("de") != "🇩🇪" {
		t.Fatal(Flag("de"))
	}
}
