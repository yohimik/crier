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
type levelWriter struct {
	level zerolog.Level
	from  string
	// log, when set, is used instead of the package's bridge logger. It is what
	// lets a scoped capture write into the logger of the call that started it.
	log *zerolog.Logger
}

func (w *levelWriter) Write(p []byte) (int, error) {
	msg := string(bytes.TrimRight(p, "\n"))
	if msg == "" {
		return len(p), nil
	}
	from := w.from
	if from == "" {
		from = "webrender"
	}
	l := w.log
	if l == nil {
		bridgeMu.RLock()
		captured := bridgeLog
		bridgeMu.RUnlock()
		l = &captured
	}
	l.WithLevel(w.level).Str("from", from).Msg(msg)
	return len(p), nil
}

// stdlogMu serialises the scoped captures below. log.SetOutput is process-wide
// state, so two of them running at once would restore each other's output.
var stdlogMu sync.Mutex

// captureStdlib redirects the standard library's default logger into l for the
// duration of the returned function, and puts it back afterwards.
//
// It exists for one library: textprocessing/fontconfig reports a missing font
// directory with a bare log.Println, which lands on standard error with no
// level, no timestamp crier chose, and no way for a caller to turn it down.
// crier's rule is that everything goes through zerolog, and a dependency's
// package-level logger is not an exception — it is the case the rule is for.
//
// Scoped rather than global: the redirect lasts only as long as the call that
// wanted it, so a program embedding crier keeps its own default logger.
func captureStdlib(l zerolog.Logger, from string, level zerolog.Level) (restore func()) {
	stdlogMu.Lock()

	prevOut := log.Writer()
	prevFlags := log.Flags()
	prevPrefix := log.Prefix()

	log.SetOutput(&levelWriter{level: level, from: from, log: &l})
	log.SetFlags(0)
	log.SetPrefix("")

	return func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
		stdlogMu.Unlock()
	}
}
