package tools

import (
	"io"
	"strings"
	"sync"
	"time"
)

const minOutputSnapshotInterval = 100 * time.Millisecond

// OutputSnapshot is a bounded, redacted view of output from one running tool.
type OutputSnapshot struct {
	ToolCallID string
	Output     string
}

// completeOutputLines withholds the current partial line. This prevents a
// secret split across writes from reaching a surface before it can be redacted.
func completeOutputLines(output string) string {
	end := strings.LastIndexAny(output, "\r\n")
	if end < 0 {
		return ""
	}
	return output[:end+1]
}

type outputThrottle struct {
	mu       sync.Mutex
	minGap   time.Duration
	lastEmit time.Time
	emitted  bool
	now      func() time.Time
}

func (t *outputThrottle) due() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if t.now != nil {
		now = t.now()
	}
	if !t.emitted || now.Sub(t.lastEmit) >= t.minGap {
		t.emitted = true
		t.lastEmit = now
		return true
	}
	return false
}

type shellSnapshotWriter struct {
	buf      io.Writer
	throttle *outputThrottle
	fire     func()
}

func (w shellSnapshotWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if err == nil && w.fire != nil && w.throttle.due() {
		w.fire()
	}
	return n, err
}

func newShellSnapshotWriters(stdout, stderr *boundedBuffer, emit func(string)) (io.Writer, io.Writer) {
	if emit == nil {
		return stdout, stderr
	}
	throttle := &outputThrottle{minGap: minOutputSnapshotInterval}
	fire := func() {
		out, errOut := stdout.retained(), stderr.retained()
		if out != "" && errOut != "" && out[len(out)-1] != '\n' {
			out += "\n"
		}
		emit(out + errOut)
	}
	return shellSnapshotWriter{buf: stdout, throttle: throttle, fire: fire},
		shellSnapshotWriter{buf: stderr, throttle: throttle, fire: fire}
}
