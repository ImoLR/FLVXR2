package logger

import (
	"fmt"
	"io"
	"path/filepath"
	"runtime"

	"github.com/go-gost/core/logger"
	"github.com/sirupsen/logrus"
)

type Options struct {
	Name   string
	Output io.Writer
	Format logger.LogFormat
	Level  logger.LogLevel
}

type Option func(opts *Options)

func NameOption(name string) Option {
	return func(opts *Options) {
		opts.Name = name
	}
}

func OutputOption(out io.Writer) Option {
	return func(opts *Options) {
		opts.Output = out
	}
}

func FormatOption(format logger.LogFormat) Option {
	return func(opts *Options) {
		opts.Format = format
	}
}

func LevelOption(level logger.LogLevel) Option {
	return func(opts *Options) {
		opts.Level = level
	}
}

// DefaultLevel is the level of a logger whose configuration names no (or an
// unknown) level. The FLVX agent defaults to warn (plan 137, QA F13): at info
// every relayed connection wrote 2–4 access lines (~690 bytes), so journald and
// /var/log grew linearly with traffic. An explicit level (gost.json
// "log": {"level": "info"}, GOST_LOGGER_LEVEL, -D/-DD) still wins.
const DefaultLevel = logger.WarnLevel

type logrusLogger struct {
	logger *logrus.Entry
	conns  *connLineLimiter
}

func NewLogger(opts ...Option) logger.Logger {
	var options Options
	for _, opt := range opts {
		opt(&options)
	}

	log := logrus.New()
	if options.Output != nil {
		log.SetOutput(options.Output)
	}

	switch options.Format {
	case logger.TextFormat:
		log.SetFormatter(&logrus.TextFormatter{
			FullTimestamp: true,
		})
	default:
		log.SetFormatter(&logrus.JSONFormatter{
			DisableHTMLEscape: true,
			// PrettyPrint:       true,
			TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		})
	}

	switch options.Level {
	case logger.TraceLevel,
		logger.DebugLevel,
		logger.InfoLevel,
		logger.WarnLevel,
		logger.ErrorLevel,
		logger.FatalLevel:
		lvl, _ := logrus.ParseLevel(string(options.Level))
		log.SetLevel(lvl)
	default:
		lvl, _ := logrus.ParseLevel(string(DefaultLevel))
		log.SetLevel(lvl)
	}

	l := &logrusLogger{
		logger: logrus.NewEntry(log),
		conns:  newConnLineLimiter(),
	}
	if options.Name != "" {
		l.logger = l.logger.WithField("logger", options.Name)
	}

	return l
}

// WithFields adds new fields to log.
func (l *logrusLogger) WithFields(fields map[string]any) logger.Logger {
	return &logrusLogger{
		logger: l.logger.WithFields(logrus.Fields(fields)),
		conns:  l.conns,
	}
}

// Trace logs a message at level Trace.
func (l *logrusLogger) Trace(args ...any) {
	l.log(logrus.TraceLevel, args...)
}

// Tracef logs a message at level Trace.
func (l *logrusLogger) Tracef(format string, args ...any) {
	l.logf(logrus.TraceLevel, format, args...)
}

// Debug logs a message at level Debug.
func (l *logrusLogger) Debug(args ...any) {
	l.log(logrus.DebugLevel, args...)
}

// Debugf logs a message at level Debug.
func (l *logrusLogger) Debugf(format string, args ...any) {
	l.logf(logrus.DebugLevel, format, args...)
}

// Info logs a message at level Info.
func (l *logrusLogger) Info(args ...any) {
	l.log(logrus.InfoLevel, args...)
}

// Infof logs a message at level Info.
func (l *logrusLogger) Infof(format string, args ...any) {
	l.logf(logrus.InfoLevel, format, args...)
}

// Warn logs a message at level Warn.
func (l *logrusLogger) Warn(args ...any) {
	l.log(logrus.WarnLevel, args...)
}

// Warnf logs a message at level Warn.
func (l *logrusLogger) Warnf(format string, args ...any) {
	l.logf(logrus.WarnLevel, format, args...)
}

// Error logs a message at level Error.
func (l *logrusLogger) Error(args ...any) {
	l.log(logrus.ErrorLevel, args...)
}

// Errorf logs a message at level Error.
func (l *logrusLogger) Errorf(format string, args ...any) {
	l.logf(logrus.ErrorLevel, format, args...)
}

// Fatal logs a message at level Fatal then the process will exit with status set to 1.
func (l *logrusLogger) Fatal(args ...any) {
	l.log(logrus.FatalLevel, args...)
	l.logger.Logger.Exit(1)
}

// Fatalf logs a message at level Fatal then the process will exit with status set to 1.
func (l *logrusLogger) Fatalf(format string, args ...any) {
	l.logf(logrus.FatalLevel, format, args...)
	l.logger.Logger.Exit(1)
}

func (l *logrusLogger) GetLevel() logger.LogLevel {
	return logger.LogLevel(l.logger.Logger.GetLevel().String())
}

func (l *logrusLogger) IsLevelEnabled(level logger.LogLevel) bool {
	lvl, _ := logrus.ParseLevel(string(level))
	return l.logger.Logger.IsLevelEnabled(lvl)
}

func (l *logrusLogger) log(level logrus.Level, args ...any) {
	lg, ok := l.entry(level)
	if !ok {
		return
	}
	lg.Log(level, args...)
}

func (l *logrusLogger) logf(level logrus.Level, format string, args ...any) {
	lg, ok := l.entry(level)
	if !ok {
		return
	}
	lg.Logf(level, format, args...)
}

// entry returns the entry to write a line at level with, or false when the
// line is dropped: disabled level, or a per-connection warn/error line over
// its budget (see connLineLimiter). The budget only applies at the default
// warn level (and above): a logger raised to info, debug or trace for
// debugging writes every line, like before.
func (l *logrusLogger) entry(level logrus.Level) (*logrus.Entry, bool) {
	lg := l.logger
	if !lg.Logger.IsLevelEnabled(level) {
		return nil, false
	}
	if lg.Logger.IsLevelEnabled(logrus.DebugLevel) {
		return lg.WithField("caller", l.caller(4)), true
	}
	if (level == logrus.WarnLevel || level == logrus.ErrorLevel) && l.conns != nil &&
		!lg.Logger.IsLevelEnabled(logrus.InfoLevel) && perConnection(lg.Data) {
		allowed, suppressed := l.conns.allow(fieldString(lg.Data, "service"))
		if !allowed {
			return nil, false
		}
		if suppressed > 0 {
			lg = lg.WithField("suppressed", suppressed)
		}
	}
	return lg, true
}

func (l *logrusLogger) caller(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		file = "<???>"
	} else {
		file = filepath.Join(filepath.Base(filepath.Dir(file)), filepath.Base(file))
	}
	return fmt.Sprintf("%s:%d", file, line)
}
