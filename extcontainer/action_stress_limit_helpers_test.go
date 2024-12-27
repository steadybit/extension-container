package extcontainer

import (
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_commons/stress"
	"github.com/stretchr/testify/assert"
	"testing"
)

func Test_adaptToCpuContainerLimits(t *testing.T) {
	type args struct {
		cpuLimitInMilliCpu int
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
		want int
	}{
		{
			name: "empty input",
			arg:  "",
			want: -1,
		},
		{
			name: "broken input",
			arg:  "x x",
			want: -1,
		},
		{
			name: "unlimited cpu",
			arg:  "max 100000\n",
			want: -1,
		},
		{
			name: "limited cpu",
			arg:  "50000 100000\n",
			want: 500,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpuMax := readCGroupV2CpuLimit("kubepods/besteffort/pod_xyz/container_xyz", mockFilesystem{
				values: map[string]string{
					"/sys/fs/cgroup/kubepods/besteffort/pod_xyz/container_xyz/cpu.max": tt.arg,
				},
			})
			assert.Equalf(t, tt.want, cpuMax, "readCGroupV2CpuLimit with file content (%v)", tt.arg)
		})
	}
}

func Test_readCGroupV1MemLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit string
		want  int
	}{
		{
			name:  "empty input",
			limit: "",
			want:  -1,
		},
		{
			name:  "broken input",
			limit: "x",
			want:  -1,
		},
		{
			name:  "unlimited mem",
			limit: fmt.Sprintf("%d\n", cgroupV1MemUnlimited),
			want:  -1,
		},
		{
			name:  "limited memory to 50MB",
			limit: "52428800\n",
			want:  52428800,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpuMax := readCGroupV1MemLimit("kubepods/besteffort/pod_xyz/container_xyz", mockFilesystem{
				values: map[string]string{
					"/sys/fs/cgroup/memory/kubepods/besteffort/pod_xyz/container_xyz/memory.limit_in_bytes": tt.limit,
				},
			})
			assert.Equalf(t, tt.want, cpuMax, "Test_readCGroupV1MemLimit() with file content (%v)", tt.limit)
		})
	}
}

func Test_adaptToMemContainerLimits(t *testing.T) {
	type args struct {
		memoryLimit    int
		givenVmWorkers int
		givenVmBytes   string
	}
	type expected struct {
		adaptedVmBytes string
	}
	tests := []struct {
		name     string
		args     args
		expected expected
	}{
		{
			name: "memory limit is 100 MB and 80% of it should be consumed",
			args: args{
				memoryLimit:    1024 * 1024 * 100,
				givenVmWorkers: 1,
				givenVmBytes:   "80%",
			},
			expected: expected{
				adaptedVmBytes: "81920K",
			},
		},
		{
			name: "memory limit is 100 MB and 80% of it should be consumed by two four workers",
			args: args{
				memoryLimit:    1024 * 1024 * 100,
				givenVmWorkers: 4,
				givenVmBytes:   "80%",
			},
			expected: expected{
				adaptedVmBytes: "20480K",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := stress.Opts{
				VmWorkers: &tt.args.givenVmWorkers,
				VmBytes:   tt.args.givenVmBytes,
			}
			adaptToMemContainerLimits(tt.args.memoryLimit, &opts)
			assert.Equal(t, tt.expected.adaptedVmBytes, opts.VmBytes)
		})
	}
}

func Test_readCGroupV2MemLimit(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want int
	}{
		{
			name: "empty input",
			arg:  "",
			want: -1,
		},
		{
			name: "broken input",
			arg:  "x x",
			want: -1,
		},
		{
			name: "unlimited memory",
			arg:  "max\n",
			want: -1,
		},
		{
			name: "limited cpu",
			arg:  "52428800\n",
			want: 52428800,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpuMax := readCGroupV2MemLimit("kubepods/besteffort/pod_xyz/container_xyz", mockFilesystem{
				values: map[string]string{
					"/sys/fs/cgroup/kubepods/besteffort/pod_xyz/container_xyz/memory.max": tt.arg,
				},
			})
			assert.Equalf(t, tt.want, cpuMax, "readCGroupV2MemLimit with file content (%v)", tt.arg)
		})
	}
}

func Test_readCGroupV1CpuLimit(t *testing.T) {
	tests := []struct {
		name   string
		quota  string
		period string
		want   int
	}{
		{
			name:   "empty input",
			quota:  "",
			period: "",
			want:   -1,
		},
		{
			name:   "broken input",
			quota:  "x",
			period: "x",
			want:   -1,
		},
		{
			name:   "unlimited cpu",
			quota:  "-1",
			period: "100000\n",
			want:   -1,
		},
		{
			name:   "limited cpu",
			quota:  "50000\n",
			period: "100000\n",
			want:   500,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpuMax := readCGroupV1CpuLimit("kubepods/besteffort/pod_xyz/container_xyz", mockFilesystem{
				values: map[string]string{
					"/sys/fs/cgroup/cpu,cpuacct/kubepods/besteffort/pod_xyz/container_xyz/cpu.cfs_quota_us":  tt.quota,
					"/sys/fs/cgroup/cpu,cpuacct/kubepods/besteffort/pod_xyz/container_xyz/cpu.cfs_period_us": tt.period,
				},
			})
			assert.Equalf(t, tt.want, cpuMax, "Test_readCGroupV1CpuLimit() with file content (%v) (%v)", tt.quota, tt.period)
		})
	}
}

type mockFilesystem struct {
	values map[string]string
}

func (m mockFilesystem) ReadFile(name string) ([]byte, error) {
	return []byte(m.values[name]), nil
}
