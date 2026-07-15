package httpapi_test

import (
	"testing"

	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
)

func TestParseBearerContract(t *testing.T) {
	tests := []struct {
		name        string
		header      string
		wantToken   string
		wantErrCode string
	}{
		{name: "missing", wantErrCode: "MISSING_AUTHORIZATION"},
		{name: "scheme only", header: "Bearer", wantErrCode: "MALFORMED_AUTHORIZATION"},
		{name: "empty credential", header: "Bearer ", wantErrCode: "MALFORMED_AUTHORIZATION"},
		{name: "wrong scheme", header: "Basic token", wantErrCode: "MALFORMED_AUTHORIZATION"},
		{name: "multipart credential", header: "Bearer one two", wantErrCode: "MALFORMED_AUTHORIZATION"},
		{name: "embedded tab", header: "Bearer one\ttwo", wantErrCode: "MALFORMED_AUTHORIZATION"},
		{name: "case insensitive scheme", header: "bEaReR opaque-token", wantToken: "opaque-token"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotToken, gotError := httpapi.ParseBearer(test.header)
			if gotToken != test.wantToken {
				t.Errorf("ParseBearer() token = %q, want %q", gotToken, test.wantToken)
			}
			if test.wantErrCode == "" {
				if gotError != nil {
					t.Fatalf("ParseBearer() error = %#v, want nil", gotError)
				}
				return
			}
			if gotError == nil || gotError.Code != test.wantErrCode {
				t.Fatalf("ParseBearer() error = %#v, want code %q", gotError, test.wantErrCode)
			}
		})
	}
}
