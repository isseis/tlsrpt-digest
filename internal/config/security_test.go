package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestRawIMAPConfig_NoCredentialFields(t *testing.T) {
	typ := reflect.TypeOf(rawIMAPConfig{})
	for i := range typ.NumField() {
		field := typ.Field(i)
		fieldName := strings.ToLower(field.Name)
		tomlName := strings.ToLower(field.Tag.Get("toml"))
		for _, forbidden := range []string{"password", "username"} {
			if strings.Contains(fieldName, forbidden) {
				t.Fatalf("rawIMAPConfig field %s contains credential name %q", field.Name, forbidden)
			}
			if strings.Contains(tomlName, forbidden) {
				t.Fatalf("rawIMAPConfig TOML tag %s contains credential name %q", tomlName, forbidden)
			}
		}
	}
}
