package files

import "testing"

func TestIsTextFile_Extensions(t *testing.T) {
	cases := []struct {
		filename string
		want     bool
	}{
		{"notes.txt", true},
		{"README.md", true},
		{"data.csv", true},
		{"config.json", true},
		{"main.py", true},
		{"main.go", true},
		{"main.c", true},
		{"main.cpp", true},
		{"index.html", true},
		{"style.css", true},
		{"app.ts", true},
		{"app.tsx", true},
		{"archive.zip", false},
		{"photo.png", false},
		{"doc.pdf", false},
		{"binary.exe", false},
		{"unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			if got := IsTextFile(tc.filename, ""); got != tc.want {
				t.Errorf("IsTextFile(%q, \"\") = %v, want %v", tc.filename, got, tc.want)
			}
		})
	}
}

func TestIsTextFile_MimeTypes(t *testing.T) {
	cases := []struct {
		filename    string
		contentType string
		want        bool
	}{
		{"", "text/plain", true},
		{"", "text/markdown", true},
		{"", "application/json", true},
		{"", "application/xml", true},
		{"", "application/javascript", true},
		{"", "application/x-yaml", true},
		{"", "application/x-www-form-urlencoded", true},
		{"", "application/pdf", false},
		{"", "image/png", false},
		{"", "application/octet-stream", false},
		{"unknown", "text/plain; charset=utf-8", true},
		{"unknown", "application/json; charset=utf-8", true},
	}
	for _, tc := range cases {
		t.Run(tc.contentType, func(t *testing.T) {
			if got := IsTextFile(tc.filename, tc.contentType); got != tc.want {
				t.Errorf("IsTextFile(%q, %q) = %v, want %v", tc.filename, tc.contentType, got, tc.want)
			}
		})
	}
}
