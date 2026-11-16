package core

import (
	"path"
	"sort"
	"strings"
)

// ProcessUsage is one process in the resource table.
type ProcessUsage struct {
	PID        int
	Name       string
	CPUPercent float64
	MemBytes   uint64
	GPUPercent float64
	GPUOk      bool
}

// AppUsage is one application group of processes.
type AppUsage struct {
	// Package is the group key: "com.foo" and "com.foo:remote" are one app.
	Package string
	// Label is the application's user-visible name; empty until the app
	// metadata cache knows it.
	Label string
	// HasIcon reports whether the cache holds an icon for this package.
	HasIcon bool
	// MetaStale reports that the cached name and icon are past their TTL.
	MetaStale  bool
	Processes  []ProcessUsage
	CPUPercent float64
	MemBytes   uint64
	GPUPercent float64
	GPUOk      bool
}

// GroupProcesses turns per-process readings into one row per application.
// Groups and the processes inside them are ordered by CPU, so the expensive
// application is the first thing the page shows.
func GroupProcesses(entries []procEntry, cpuPercent map[int]float64) []AppUsage {
	groups := map[string]*AppUsage{}
	for _, entry := range entries {
		key := appKey(entry.Command)
		if key == "" {
			continue
		}
		app := groups[key]
		if app == nil {
			app = &AppUsage{Package: key}
			groups[key] = app
		}
		app.Processes = append(app.Processes, ProcessUsage{
			PID:        entry.PID,
			Name:       entry.Command,
			CPUPercent: cpuPercent[entry.PID],
			MemBytes:   entry.RSS,
		})
	}

	apps := make([]AppUsage, 0, len(groups))
	for _, app := range groups {
		sort.SliceStable(app.Processes, func(i, j int) bool {
			if app.Processes[i].CPUPercent != app.Processes[j].CPUPercent {
				return app.Processes[i].CPUPercent > app.Processes[j].CPUPercent
			}
			return app.Processes[i].PID < app.Processes[j].PID
		})
		for _, process := range app.Processes {
			app.CPUPercent += process.CPUPercent
			app.MemBytes += process.MemBytes
			if process.GPUOk {
				app.GPUPercent += process.GPUPercent
				app.GPUOk = true
			}
		}
		apps = append(apps, *app)
	}
	sort.SliceStable(apps, func(i, j int) bool {
		if apps[i].CPUPercent != apps[j].CPUPercent {
			return apps[i].CPUPercent > apps[j].CPUPercent
		}
		return apps[i].Package < apps[j].Package
	})
	return apps
}

// appKey names the application a command belongs to: the executable's file
// name without any ":suffix" the app gave its extra process.
func appKey(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	name := path.Base(fields[0])
	if before, _, found := strings.Cut(name, ":"); found {
		name = before
	}
	return name
}
