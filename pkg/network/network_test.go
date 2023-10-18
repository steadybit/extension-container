package network

import (
	"context"
	"github.com/steadybit/action-kit/go/action_kit_commons/networkutils"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/utils"
	"github.com/stretchr/testify/mock"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	blackholeOpts = networkutils.BlackholeOpts{
		Filter: networkutils.Filter{
			Include: []networkutils.NetWithPortRange{
				{
					Net:       networkutils.NetAny[0],
					PortRange: networkutils.PortRangeAny,
				},
			},
		},
	}

	targetContainerConfig = utils.TargetContainerConfig{ContainerID: "fakeid"}
)

func Test_generateAndRunCommands_should_serialize(t *testing.T) {
	sidecarImagePath = func() string { return "__mocked__" }
	defer func() { sidecarImagePath = utils.SidecarImagePath }()

	var concurrent int64
	runcMock := &MockedRunc{}
	runcMock.On("Create", mock.Anything, mock.Anything, mock.Anything).Return("", func() error { return nil }, nil)
	runcMock.On("EditSpec", mock.Anything, mock.Anything).Return(nil)
	runcMock.On("Run", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		counter := atomic.AddInt64(&concurrent, 1)
		defer func() { atomic.AddInt64(&concurrent, -1) }()
		if counter > 1 {
			t.Errorf("concurrent run detected")
		}
		time.Sleep(10 * time.Millisecond)
	}).Return(nil)
	runcMock.On("Delete", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	wg := sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = generateAndRunCommands(context.Background(), runcMock, targetContainerConfig, &blackholeOpts, networkutils.ModeAdd)
		}()
	}
	wg.Wait()
}

type MockedRunc struct {
	mock.Mock
}
type MockedBundle struct {
	mock.Mock
}

func (m *MockedRunc) State(ctx context.Context, id string) (*runc.ContainerState, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*runc.ContainerState), args.Error(1)
}

func (b *MockedBundle) EditSpec(ctx context.Context, editors ...runc.SpecEditor) error {
	args := b.Called(ctx, editors)
	return args.Error(0)
}

func (m *MockedRunc) Run(ctx context.Context, bundle runc.ContainerBundle, ioOpts runc.IoOpts) error {
	args := m.Called(ctx, bundle, ioOpts)
	return args.Error(0)
}

func (m *MockedRunc) Delete(ctx context.Context, id string, force bool) error {
	args := m.Called(ctx, id, force)
	return args.Error(0)
}

func (m *MockedRunc) Create(ctx context.Context, image, id string) (runc.ContainerBundle, error) {
	args := m.Called(ctx, image, id)
	return args.Get(1).(runc.ContainerBundle), args.Error(2)
}
