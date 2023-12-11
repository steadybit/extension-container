package extcontainer

import (
	"github.com/steadybit/action-kit/go/action_kit_commons/stress"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/stretchr/testify/assert"
	"testing"
)

func Test_adaptToCpuContainerLimits(t *testing.T) {
	type args struct {
		cpuLimitInMilliCpu float64
		cpuCount           int
		givenCpuWorkers    int
		givenCpuLoad       int
	}
	type expected struct {
		adaptedCpuWorkers int
		adaptedCpuLoad    int
	}
	tests := []struct {
		name     string
		args     args
		expected expected
	}{
		{
			name: "worker-count not specified, desired cpu load can be handled by one worker",
			args: args{
				cpuLimitInMilliCpu: 200,
				cpuCount:           4,
				givenCpuLoad:       100,
				givenCpuWorkers:    0,
			},
			expected: expected{
				adaptedCpuLoad:    20,
				adaptedCpuWorkers: 1,
			},
		},
		{
			name: "worker-count not specified, desired cpu load needs multiple workers",
			args: args{
				cpuLimitInMilliCpu: 1500,
				cpuCount:           4,
				givenCpuLoad:       100,
				givenCpuWorkers:    0,
			},
			expected: expected{
				adaptedCpuLoad:    75,
				adaptedCpuWorkers: 2,
			},
		},
		{
			name: "worker-count not specified, desired 60% cpu fits to single worker",
			args: args{
				cpuLimitInMilliCpu: 1500,
				cpuCount:           4,
				givenCpuLoad:       60,
				givenCpuWorkers:    0,
			},
			expected: expected{
				adaptedCpuLoad:    90,
				adaptedCpuWorkers: 1,
			},
		},
		{
			name: "worker-count specified, desired 60% cpu is spread across workers",
			args: args{
				cpuLimitInMilliCpu: 1500,
				cpuCount:           4,
				givenCpuLoad:       60,
				givenCpuWorkers:    3,
			},
			expected: expected{
				adaptedCpuLoad:    30,
				adaptedCpuWorkers: 3,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := stress.Opts{
				CpuWorkers: &tt.args.givenCpuWorkers,
				CpuLoad:    tt.args.givenCpuLoad,
			}
			adaptToCpuContainerLimits(tt.args.cpuLimitInMilliCpu, tt.args.cpuCount, &opts)
			assert.Equal(t, tt.expected.adaptedCpuWorkers, *opts.CpuWorkers)
			assert.Equal(t, tt.expected.adaptedCpuLoad, opts.CpuLoad)
		})
	}
}

func Test_readCGroupV2CpuLimit(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want *float64
	}{
		{
			name: "empty input",
			arg:  "",
			want: nil,
		},
		{
			name: "broken input",
			arg:  "x x",
			want: nil,
		},
		{
			name: "unlimited cpu",
			arg:  "max 100000\n",
			want: nil,
		},
		{
			name: "limited cpu",
			arg:  "50000 100000\n",
			want: extutil.Ptr(float64(500)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpuMax := readCGroupV2CpuLimitInternal("kubepods/besteffort/pod_xyz/container_xyz", mockFilesystem{
				values: map[string]string{
					"/sys/fs/cgroup/kubepods/besteffort/pod_xyz/container_xyz/cpu.max": tt.arg,
				},
			})
			if tt.want == nil {
				assert.Nil(t, cpuMax, "readCGroupV2CpuLimit with file content (%v)", tt.arg)
			} else {
				assert.Equalf(t, *tt.want, *cpuMax, "readCGroupV2CpuLimit with file content (%v)", tt.arg)
			}
		})
	}
}

func Test_readCGroupV1CpuLimit(t *testing.T) {
	tests := []struct {
		name   string
		quota  string
		period string
		want   *float64
	}{
		{
			name:   "empty input",
			quota:  "",
			period: "",
			want:   nil,
		},
		{
			name:   "broken input",
			quota:  "x",
			period: "x",
			want:   nil,
		},
		{
			name:   "unlimited cpu",
			quota:  "-1",
			period: "100000\n",
			want:   nil,
		},
		{
			name:   "limited cpu",
			quota:  "50000\n",
			period: "100000\n",
			want:   extutil.Ptr(float64(500)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpuMax := readCGroupV1CpuLimitInternal("kubepods/besteffort/pod_xyz/container_xyz", mockFilesystem{
				values: map[string]string{
					"/sys/fs/cgroup/cpu,cpuacct/kubepods/besteffort/pod_xyz/container_xyz/cpu.cfs_quota_us":  tt.quota,
					"/sys/fs/cgroup/cpu,cpuacct/kubepods/besteffort/pod_xyz/container_xyz/cpu.cfs_period_us": tt.period,
				},
			})
			if tt.want == nil {
				assert.Nil(t, cpuMax, "Test_readCGroupV1CpuLimit() with file content (%v) (%v)", tt.quota, tt.period)
			} else {
				assert.Equalf(t, *tt.want, *cpuMax, "Test_readCGroupV1CpuLimit() with file content (%v) (%v)", tt.quota, tt.period)
			}
		})
	}
}

type mockFilesystem struct {
	values map[string]string
}

func (m mockFilesystem) ReadFile(name string) ([]byte, error) {
	return []byte(m.values[name]), nil
}
