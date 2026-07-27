package api

import "sync"

var devContainers sync.Map

func trackDevContainer(appName, containerID string) {
	devContainers.Store(appName, containerID)
}

func getDevContainer(appName string) (string, bool) {
	v, ok := devContainers.Load(appName)
	if !ok {
		return "", false
	}
	return v.(string), true
}

func untrackDevContainer(appName string) {
	devContainers.Delete(appName)
}
