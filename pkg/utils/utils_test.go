package utils

import (
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

const (
	presentInode     = uint64(1)
	nonExistentInode = uint64(9999)
	nonExistentPath  = "/doesnt-exist"
	resolvedPath     = "/resolved"
)

func Test_RefreshNamespacesUsingInode(t *testing.T) {
	listNamespaceUsingInode = fakeListNamespaceUsingInode
	defer func() { listNamespaceUsingInode = listNamespaceUsingInodeImpl }()

	tests := []struct {
		name     string
		ns       []LinuxNamespaceWithInode
		wantedNs []LinuxNamespaceWithInode
	}{
		{
			name: "do nothing on nil",
		},
		{
			name: "do nothing on missing inode",
			ns: []LinuxNamespaceWithInode{{
				Inode: 0,
			}},
			wantedNs: []LinuxNamespaceWithInode{{
				Inode: 0,
			}},
		},
		{
			name: "do nothing on present path",
			ns: []LinuxNamespaceWithInode{{
				LinuxNamespace: specs.LinuxNamespace{
					Path: filepath.Join("proc", strconv.Itoa(os.Getpid()), "ns", "net"),
				},
				Inode: nonExistentInode,
			}},
			wantedNs: []LinuxNamespaceWithInode{{
				LinuxNamespace: specs.LinuxNamespace{
					Path: filepath.Join("proc", strconv.Itoa(os.Getpid()), "ns", "net"),
				},
				Inode: nonExistentInode,
			}},
		},
		{
			name: "resolv using lsns on non-existent path",
			ns: []LinuxNamespaceWithInode{{
				LinuxNamespace: specs.LinuxNamespace{
					Path: nonExistentPath,
				},
				Inode: presentInode,
			}},
			wantedNs: []LinuxNamespaceWithInode{{
				LinuxNamespace: specs.LinuxNamespace{
					Path: resolvedPath,
				},
				Inode: presentInode,
			}},
		},
		{
			name: "resolv using lsns on non-existent path fails",
			ns: []LinuxNamespaceWithInode{{
				LinuxNamespace: specs.LinuxNamespace{
					Path: nonExistentPath,
				},
				Inode: nonExistentInode,
			}},
			wantedNs: []LinuxNamespaceWithInode{{
				LinuxNamespace: specs.LinuxNamespace{
					Path: nonExistentPath,
				},
				Inode: nonExistentInode,
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RefreshNamespacesUsingInode(context.Background(), tt.ns)
			assert.Equal(t, tt.wantedNs, tt.ns)
		})
	}
}

func fakeListNamespaceUsingInode(_ context.Context, inode uint64) (string, error) {
	if inode == presentInode {
		return resolvedPath, nil
	}
	return "", fmt.Errorf("no such inode")
}
