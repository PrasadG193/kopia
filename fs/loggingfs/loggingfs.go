// Package loggingfs implements a wrapper that logs all filesystem actions.
package loggingfs

import (
	"time"

	"github.com/rs/zerolog/log"

	"github.com/kopia/kopia/fs"
)

type loggingOptions struct {
	printf func(fmt string, args ...interface{})
	prefix string
}

type loggingDirectory struct {
	options *loggingOptions
	fs.Directory
}

func (ld *loggingDirectory) Readdir() (fs.Entries, error) {
	t0 := time.Now()
	entries, err := ld.Directory.Readdir()
	dt := time.Since(t0)
	ld.options.printf(ld.options.prefix+"Readdir(%v) took %v and returned %v items", fs.EntryPath(ld), dt, len(entries))
	loggingEntries := make(fs.Entries, len(entries))
	for i, entry := range entries {
		loggingEntries[i] = wrapWithOptions(entry, ld.options)
	}
	return loggingEntries, err
}

type loggingFile struct {
	options *loggingOptions
	fs.File
}

type loggingSymlink struct {
	options *loggingOptions
	fs.Symlink
}

// Option modifies the behavior of logging wrapper.
type Option func(o *loggingOptions)

// Wrap returns an Entry that wraps another Entry and logs all method calls.
func Wrap(e fs.Entry, options ...Option) fs.Entry {
	return wrapWithOptions(e, applyOptions(options))
}

func wrapWithOptions(e fs.Entry, opts *loggingOptions) fs.Entry {
	switch e := e.(type) {
	case fs.Directory:
		return fs.Directory(&loggingDirectory{opts, e})

	case fs.File:
		return fs.File(&loggingFile{opts, e})

	case fs.Symlink:
		return fs.Symlink(&loggingSymlink{opts, e})

	default:
		return e
	}
}

func applyOptions(opts []Option) *loggingOptions {
	o := &loggingOptions{
		printf: log.Printf,
	}
	for _, f := range opts {
		f(o)
	}
	return o
}

// Output is an option that causes all output to be sent to a given function instead of log.Printf()
func Output(outputFunc func(fmt string, args ...interface{})) Option {
	return func(o *loggingOptions) {
		o.printf = outputFunc
	}
}

// Prefix specifies prefix to be prepended to all log output.
func Prefix(prefix string) Option {
	return func(o *loggingOptions) {
		o.prefix = prefix
	}
}

var _ fs.Directory = &loggingDirectory{}
var _ fs.File = &loggingFile{}
var _ fs.Symlink = &loggingSymlink{}
