package models

import "testing"

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Price Watch", "price-watch"},
		{"my task", "my-task"},
		{"Hello World 123", "hello-world-123"},
		{"UPPERCASE", "uppercase"},
		{"already-slug", "already-slug"},
		{"with_underscore", "with-underscore"},
		{"  spaces  ", "spaces"},
		{"a", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Slugify(tt.input)
			if got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSlugify_SpecialChars(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello (World)", "hello-world"},
		{"test@home", "testhome"},
		{"a.b.c", "abc"},
		{"price$alert!", "pricealert"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Slugify(tt.input)
			if got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
