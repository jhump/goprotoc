package internal

import (
	"testing"

	"github.com/pedgeio/goprotoctest/goprotoctest"
)

func TestGoogleapisIncludeImports(t *testing.T) {
	goprotoctest.RunGoogleapisTest(
		t,
		goprotoctest.RunGoogleapisTestOptions{
			CacheDirPath:      "cache",
			GoogleapisRef:     "0537189470f04f24836d6959821c24197a0ed120",
			IncludeImports:    true,
			IncludeSourceInfo: false,
			IgnoreGoogleapisPackages: map[string]struct{}{
				// syntax error with multiple lines
				"google/ads/googleads/v1/services": struct{}{},
			},
		},
	)
}
