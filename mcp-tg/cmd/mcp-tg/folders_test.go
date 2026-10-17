package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/telegram"
)

func TestWriteFolders(t *testing.T) {
	tests := []struct {
		name       string
		folders    []telegram.Folder
		want       string
		wantParsed []config.Folder
	}{
		{
			name:    "папки без категорий",
			folders: []telegram.Folder{{ID: 3, Title: "Claude"}, {ID: 7, Title: "Shared"}},
			want: `folders:
  - id: 3
    title: Claude
  - id: 7
    title: Shared
`,
			wantParsed: []config.Folder{{ID: 3, Title: "Claude"}, {ID: 7, Title: "Shared"}},
		},
		{
			name:    "папка с категориями получает комментарий внутри записи",
			folders: []telegram.Folder{{ID: 5, Title: "Work", HasCategories: true}, {ID: 3, Title: "Claude"}},
			want: `folders:
  - id: 5
    title: Work
    # categories are ignored, only chats added to the folder explicitly are read
  - id: 3
    title: Claude
`,
			wantParsed: []config.Folder{{ID: 5, Title: "Work"}, {ID: 3, Title: "Claude"}},
		},
		{
			name:    "заголовок с двоеточием берётся в кавычки",
			folders: []telegram.Folder{{ID: 5, Title: "Work: all"}},
			want: `folders:
  - id: 5
    title: 'Work: all'
`,
			wantParsed: []config.Folder{{ID: 5, Title: "Work: all"}},
		},
		{
			name:    "пустой заголовок опускается, комментарий после id",
			folders: []telegram.Folder{{ID: 8, HasCategories: true}},
			want: `folders:
  - id: 8
    # categories are ignored, only chats added to the folder explicitly are read
`,
			wantParsed: []config.Folder{{ID: 8}},
		},
		{
			name:       "без папок",
			want:       "folders: []\n",
			wantParsed: []config.Folder{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer

			err := writeFolders(&out, tt.folders)

			require.NoError(t, err)
			require.Equal(t, tt.want, out.String())
			cfg, err := config.Parse([]byte("session_path: /s\n" + out.String()))
			require.NoError(t, err)
			require.Equal(t, tt.wantParsed, cfg.Folders)
		})
	}
}
