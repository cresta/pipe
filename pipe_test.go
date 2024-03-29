package pipe_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/cresta/pipe"
	"github.com/stretchr/testify/require"
)

func TestShell(t *testing.T) {
	require.NoError(t, pipe.Shell("echo hi").Run(context.Background()))
}

func TestNewPiped(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, pipe.NewPiped("echo", "hi").Execute(context.Background(), nil, &buf, nil))
	require.Contains(t, buf.String(), "hi")
}

func TestEnv(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, pipe.Shell("GONOSUMDB=testing env").Execute(context.Background(), nil, &buf, nil))
	require.Contains(t, buf.String(), "testing")
}

func TestPipeTo(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, pipe.Shell("echo hi").PipeTo(pipe.NewPiped("cat")).Execute(context.Background(), nil, &buf, nil))
	require.Contains(t, buf.String(), "hi")
}
