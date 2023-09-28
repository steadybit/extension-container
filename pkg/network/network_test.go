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

	targetContainerConfig = TargetContainerConfig{ContainerID: "fakeid"}
)

func Test_generateAndRunCommands_should_serialize(t *testing.T) {
	sidecarImagePath = func() string { return "__mocked__" }
	defer func() { sidecarImagePath = utils.SidecarImagePath }()

	var concurrent int64
	runcMock := &MockedRunc{}
	runcMock.On("PrepareBundle", mock.Anything, mock.Anything, mock.Anything).Return("", func() error { return nil }, nil)
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

func (m *MockedRunc) State(ctx context.Context, id string) (*runc.Container, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*runc.Container), args.Error(1)
}

func (m *MockedRunc) Spec(ctx context.Context, bundle string) error {
	args := m.Called(ctx, bundle)
	return args.Error(0)
}

func (m *MockedRunc) EditSpec(ctx context.Context, bundle string, editors ...runc.SpecEditor) error {
	args := m.Called(bundle, editors)
	return args.Error(0)
}

func (m *MockedRunc) Run(ctx context.Context, id, bundle string, ioOpts runc.IoOpts) error {
	args := m.Called(ctx, id, bundle, ioOpts)
	return args.Error(0)
}

func (m *MockedRunc) Delete(ctx context.Context, id string, force bool) error {
	args := m.Called(ctx, id, force)
	return args.Error(0)
}

func (m *MockedRunc) PrepareBundle(ctx context.Context, image string, id string) (string, func() error, error) {
	args := m.Called(ctx, image, id)
	return args.String(0), args.Get(1).(func() error), args.Error(2)
}
