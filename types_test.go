package main

import (
	"testing"
	"time"
)

func TestDuration_String(t *testing.T) {
	type fields struct {
		Duration time.Duration
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{"123ms", fields{time.Millisecond * 123}, "120ms"},
		{"1.23s", fields{time.Millisecond * 1230}, "1.23s"},
		{"12.3s", fields{time.Millisecond * 12300}, "12.3s"},
		{"2m3s", fields{time.Millisecond * 123000}, "2m3s"},
		{"2h3m4s", fields{time.Millisecond * 7384000}, "2h3m4s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Duration{
				Duration: tt.fields.Duration,
			}
			if got := d.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}
