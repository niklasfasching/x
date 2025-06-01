package util

import (
	"context"
	"fmt"
	"sync"
)

type Logger struct {
	fs []logFn
	sync.Mutex
}

type logFn func(lvl Lvl, msg string)
type Lvl int

const (
	DEBUG Lvl = iota
	INFO
	WARN
	ERROR
)

func Debug(ctx context.Context, args ...any)              { Print(ctx, DEBUG, args...) }
func Debugf(ctx context.Context, tpl string, args ...any) { Printf(ctx, DEBUG, tpl, args...) }
func Info(ctx context.Context, args ...any)               { Print(ctx, INFO, args...) }
func Infof(ctx context.Context, tpl string, args ...any)  { Printf(ctx, INFO, tpl, args...) }
func Warn(ctx context.Context, args ...any)               { Print(ctx, WARN, args...) }
func Warnf(ctx context.Context, tpl string, args ...any)  { Printf(ctx, WARN, tpl, args...) }
func Error(ctx context.Context, args ...any)              { Print(ctx, ERROR, args...) }
func Errorf(ctx context.Context, tpl string, args ...any) { Printf(ctx, ERROR, tpl, args...) }

func WithLogger(ctx context.Context, fs ...logFn) context.Context {
	l, ok := GetLogger(ctx)
	if !ok {
		return context.WithValue(ctx, Lvl(-1), &Logger{fs: fs})
	}
	l.Lock()
	l.fs = append(l.fs, fs...)
	l.Unlock()
	return ctx
}

func GetLogger(ctx context.Context) (*Logger, bool) {
	l, ok := ctx.Value(Lvl(-1)).(*Logger)
	return l, ok
}

func WithLvl(minLvl Lvl, f logFn) logFn {
	return func(lvl Lvl, msg string) {
		if lvl >= minLvl {
			f(lvl, msg)
		}
	}
}

func Print(ctx context.Context, lvl Lvl, args ...any) {
	if l, ok := GetLogger(ctx); ok {
		l.Lock()
		defer l.Unlock()
		for _, f := range l.fs {
			f(lvl, fmt.Sprint(args...))
		}
	}
}

func Printf(ctx context.Context, lvl Lvl, tpl string, args ...any) {
	if l, ok := GetLogger(ctx); ok {
		l.Lock()
		defer l.Unlock()
		for _, f := range l.fs {
			f(lvl, fmt.Sprintf(tpl, args...))
		}
	}
}

func (l Lvl) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		panic(fmt.Errorf("bad lvl: %d", l))
	}
}

func ParseLvl(l string) Lvl {
	switch l {
	case "ERROR":
		return ERROR
	case "WARN":
		return WARN
	case "INFO":
		return INFO
	default:
		return DEBUG
	}
}
