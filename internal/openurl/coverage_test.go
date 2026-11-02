package openurl

import "testing"

func TestValidateRejectsURLWithoutHost(t *testing.T) {
	if err := validate("http:///missing-host"); err == nil {
		t.Fatal("URL without a host should be rejected")
	}
}
