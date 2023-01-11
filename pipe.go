package pipe

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/google/shlex"
)

type PipedCmd struct {
	cmd      string
	args     []string
	env      []string
	dir      string
	readFrom *PipedCmd
	pipeTo   *PipedCmd
}

func NewPiped(cmd string, args ...string) *PipedCmd {
	return &PipedCmd{
		cmd:  cmd,
		args: args,
	}
}

// Shell tries to be like the *sh shell to create a piped command.  It will, after splitting the string, run os.Expand
// on the parts.  It works correctly for things like this
//
//	Shell("echo hi")
//	Shell("GOOS=linux go build")
//	Shell("docker run -it ubuntu")
//	Shell("docker run -v $HOME/.aws:/root/.aws:ro ubuntu")
//
// It will not work like bash for things like this
//
//	Shell("echo '$HOME'")
//
// Since it will first split echo into $HOME, and then escape the HOME
func Shell(fullLine string) *PipedCmd {
	ret, err := ShellWithError(fullLine)
	if err != nil {
		panic(err)
	}
	return ret
}

func ShellWithError(fullLine string) (*PipedCmd, error) {
	parts, err := shlex.Split(fullLine)
	if err != nil {
		return nil, err
	}
	// look for environment assignments at the front
	envAssignments := make([]string, 0, len(parts))
	envMap := make(map[string]string)
	for len(parts) > 0 {
		first := parts[0]
		envSplit := strings.SplitN(first, "=", 2)
		if len(envSplit) != 2 {
			break
		}
		if len(envSplit[0]) == 0 {
			break
		}
		envAssignments = append(envAssignments, first)
		envMap[envSplit[0]] = envSplit[1]
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("bad command line %s", fullLine)
	}
	prog := parts[0]
	// Run environment expansion on all the arguments
	args := parts[1:]
	for idx := range args {
		args[idx] = os.Expand(args[idx], func(s string) string {
			if v, exists := envMap[s]; exists {
				return v
			}
			return os.Getenv(s)
		})
	}

	return &PipedCmd{
		cmd:  prog,
		args: args,
		env:  envAssignments,
	}, nil
}

func (p *PipedCmd) WithEnv(e []string) *PipedCmd {
	p.env = e
	return p
}

func (p *PipedCmd) WithDir(d string) *PipedCmd {
	p.dir = d
	return p
}

func (p *PipedCmd) Shell(fullLine string) *PipedCmd {
	next := Shell(fullLine)
	return p.PipeTo(next)
}

func (p *PipedCmd) PipeTo(into *PipedCmd) *PipedCmd {
	if p.readFrom != nil {
		panic("pipe already set to read")
	}
	if p.pipeTo != nil {
		panic("pipe already set to pipe to")
	}
	if into.readFrom != nil {
		panic("into is already set to read")
	}
	into.readFrom = p
	p.pipeTo = into
	return into
}

func (p *PipedCmd) Pipe(cmd string, args ...string) *PipedCmd {
	return p.PipeTo(&PipedCmd{
		cmd:  cmd,
		args: args,
	})
}

func (p *PipedCmd) Run(ctx context.Context) error {
	return p.Execute(ctx, nil, os.Stdout, os.Stderr)
}

func (p *PipedCmd) Execute(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	cmdCtx, withCancel := context.WithCancel(ctx)
	defer withCancel()
	// Setup and start each command
	commands := make([]*exec.Cmd, 0)
	for current := p; current != nil; current = current.readFrom {
		//nolint:gosec
		cmd := exec.CommandContext(cmdCtx, current.cmd, current.args...)
		cmd.Stderr = stderr
		cmd.Env = current.env
		cmd.Dir = p.dir
		// put the last Pipe() at the first of commands
		commands = append([]*exec.Cmd{cmd}, commands...)
	}
	for idx := range commands {
		if idx == 0 {
			commands[idx].Stdin = stdin
		} else {
			p, err := commands[idx-1].StdoutPipe()
			if err != nil {
				return fmt.Errorf("unable to get stdout pipe: %w", err)
			}
			commands[idx].Stdin = p
		}
		if idx == len(commands)-1 {
			commands[idx].Stdout = stdout
		}
	}
	for idx, cmd := range commands {
		if err := cmd.Start(); err != nil {
			withCancel()
			// Wait for the previous commands to finish so we do not leak
			for i := 0; i < idx; i++ {
				_ = commands[i].Wait()
			}
			return fmt.Errorf("unable to start command: %w", err)
		}
	}
	var waitErr error
	for i := len(commands) - 1; i >= 0; i-- {
		// https://golang.org/pkg/os/exec/#Cmd.StdoutPipe
		// "It is thus incorrect to call Wait before all reads from the pipe have completed"
		// So we need to Wait for the last in the chain first
		cmd := commands[i]
		if err := cmd.Wait(); err != nil {
			// We will end up returning the *last* wait error, which will be the first command of the pipes that failed
			waitErr = err
			withCancel()
		}
	}
	return waitErr
}
