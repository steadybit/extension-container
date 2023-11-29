package diskfill

import (
	"reflect"
	"testing"
)

func Test_calculateDiskUsage(t *testing.T) {
	type args struct {
		lines []string
	}
	tests := []struct {
		name    string
		args    args
		want    space
		wantErr bool
	}{
		{
			name: "success",
			args: args{
				lines: []string{
					"Filesystem     1K-blocks     Used Available Use% Mounted on",
					"overlay        61252480  1024000  60228480   2% /",
				},
			},
			want: space{
				capacity:  61252480,
				used:      1024000,
				available: 60228480,
			},
			wantErr: false,
		},{
			name: "fail",
			args: args{
				lines: []string{
					"Filesystem     2K-blocks     Used Available Use% Mounted on",
					"overlay        61252480  1024000  60228480   2% /",
				},
			},
			
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := calculateDiskUsage(tt.args.lines)
			if (err != nil) != tt.wantErr {
				t.Errorf("calculateDiskUsage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("calculateDiskUsage() got = %v, want %v", got, tt.want)
			}
		})
	}
}
