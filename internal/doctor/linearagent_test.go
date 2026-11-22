package doctor

import (
	"reflect"
	"testing"
)

func TestMissingScopes(t *testing.T) {
	for _, tc := range []struct {
		granted string
		want    []string
	}{
		{"", nil},
		{"read,write,app:assignable,app:mentionable", nil},
		{"read write app:assignable app:mentionable", nil},
		{"read,write", []string{"app:assignable", "app:mentionable"}},
	} {
		if got := missingScopes(tc.granted); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %v want %v", tc.granted, got, tc.want)
		}
	}
}
