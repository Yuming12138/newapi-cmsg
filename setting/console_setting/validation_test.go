package console_setting

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestValidateAnnouncementsCountsCharacters(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{name: "500 Chinese characters", content: strings.Repeat("公告", 250)},
		{name: "501 Chinese characters", content: strings.Repeat("公告", 250) + "中", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := common.Marshal([]map[string]string{{
				"content":     tt.content,
				"publishDate": "2026-09-29T00:00:00Z",
			}})
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateConsoleSettings(string(payload), "Announcements")
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateConsoleSettings() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
