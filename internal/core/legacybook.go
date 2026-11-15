package core

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// maxLegacyEntrySize is the largest tailcat-adb entry file the importer reads.
const maxLegacyEntrySize = 8192

var legacyUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// LegacyEntry is one imported device from a tailcat-adb address book.
type LegacyEntry struct {
	Username string
	Address  string
}

// LegacyImport is the result of reading an address book directory.
type LegacyImport struct {
	Entries []LegacyEntry
	Skipped int
}

// ReadLegacyBook reads <root>/users, a directory of one file per device whose
// name is the device label and whose content is the Tailcat address. The
// import is read-only: nothing is written, moved or deleted.
func ReadLegacyBook(root string) (LegacyImport, error) {
	var out LegacyImport
	if err := rejectSymlink(root); err != nil {
		return out, err
	}
	users := filepath.Join(root, "users")
	if err := rejectSymlink(users); err != nil {
		return out, err
	}
	info, err := os.Stat(users)
	if err != nil || !info.IsDir() {
		return out, Errorf("未找到 tailcat-adb 地址簿：%s", users)
	}
	entries, err := os.ReadDir(users)
	if err != nil {
		return out, Errorf("无法读取地址簿：%v", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		name := entry.Name()
		if !legacyUsernamePattern.MatchString(name) {
			out.Skipped++
			continue
		}
		fileInfo, err := os.Lstat(filepath.Join(users, name))
		if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Size() > maxLegacyEntrySize {
			out.Skipped++
			continue
		}
		raw, err := os.ReadFile(filepath.Join(users, name))
		if err != nil {
			out.Skipped++
			continue
		}
		address := strings.TrimSpace(string(raw))
		if !Plausible(address) {
			out.Skipped++
			continue
		}
		out.Entries = append(out.Entries, LegacyEntry{Username: name, Address: address})
	}
	return out, nil
}
