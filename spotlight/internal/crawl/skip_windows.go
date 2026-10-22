//go:build windows

package crawl

import (
	"io/fs"
	"syscall"
)

const (
	attrReparsePoint       = 0x400
	attrOffline            = 0x1000
	attrRecallOnOpen       = 0x40000
	attrRecallOnDataAccess = 0x400000
)

// Skip reports whether an entry is a reparse point (junction, symlink) or a
// cloud placeholder such as an OneDrive "online-only" file. Reading those
// would trigger a download, so they are not indexed.
func Skip(info fs.FileInfo) bool {
	d, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	mask := uint32(attrReparsePoint | attrOffline | attrRecallOnOpen | attrRecallOnDataAccess)
	return d.FileAttributes&mask != 0
}
