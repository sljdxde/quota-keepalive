package openurl

import "testing"

func TestValidateAcceptsOnlyWebURLs(t *testing.T) {
	for _, raw := range []string{
		"https://www.workbuddy.cn",
		"http://127.0.0.1:8080/login",
	} {
		if err := validate(raw); err != nil {
			t.Errorf("有效网页 URL 被拒绝 %q: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"file:///tmp/secret",
		"javascript:alert(1)",
		"workbuddy://callback",
		"not a url",
	} {
		if err := validate(raw); err == nil {
			t.Errorf("非网页 URL 必须拒绝 %q", raw)
		}
	}
}
