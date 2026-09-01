package render

import (
	"bytes"
	"log"
	"sync"

	wlog "github.com/benoitkugler/webrender/logger"
	"github.com/rs/zerolog"
)

var (
	bridgeOnce sync.Once
	bridgeMu   sync.RWMutex
	bridgeLog  = zerolog.Nop()
)

// CaptureLogs routes webrender's two package-level loggers into zerolog.
//
// They are globals writing to standard output, and standard output is where
// crier prints results a script parses. Leaving them alone would put
// "webrender.progress: Step 4" in the middle of a JSON report, so they are
// redirected once, at startup: progress lines become trace records and
// warnings — unsupported CSS, a font that would not load — become warnings.
func CaptureLogs(l zerolog.Logger) {
	bridgeMu.Lock()
	bridgeLog = l
	bridgeMu.Unlock()

	bridgeOnce.Do(func() {
		wlog.ProgressLogger = log.New(&levelWriter{level: zerolog.TraceLevel}, "", 0)
		wlog.WarningLogger = log.New(&levelWriter{level: zerolog.WarnLevel}, "", 0)
	})
}

// levelWriter turns each line the standard logger writes into one zerolog
// record.
type levelWriter struct{ level zerolog.Level }

func (w *levelWriter) Write(p []byte) (int, error) {
	msg := string(bytes.TrimRight(p, "\n"))
	if msg != "" {
		bridgeMu.RLock()
		l := bridgeLog
		bridgeMu.RUnlock()
		l.WithLevel(w.level).Str("from", "webrender").Msg(msg)
	}
	return len(p), nil
}
