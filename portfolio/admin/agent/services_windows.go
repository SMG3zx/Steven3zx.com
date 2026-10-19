package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var serviceStates = map[uint32]string{
	windows.SERVICE_STOPPED:          "stopped",
	windows.SERVICE_START_PENDING:    "starting",
	windows.SERVICE_STOP_PENDING:     "stopping",
	windows.SERVICE_RUNNING:          "running",
	windows.SERVICE_CONTINUE_PENDING: "continuing",
	windows.SERVICE_PAUSE_PENDING:    "pausing",
	windows.SERVICE_PAUSED:           "paused",
}

var startTypes = map[uint32]string{
	windows.SERVICE_BOOT_START:   "boot",
	windows.SERVICE_SYSTEM_START: "system",
	windows.SERVICE_AUTO_START:   "automatic",
	windows.SERVICE_DEMAND_START: "manual",
	windows.SERVICE_DISABLED:     "disabled",
}

// listServices enumerates Win32 services with only enumerate/query rights,
// so it works without running the agent elevated (golang.org/x/sys/windows/svc/mgr
// asks for SC_MANAGER_ALL_ACCESS, which needs admin).
func listServices() ([]Service, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, err
	}
	defer windows.CloseServiceHandle(scm)

	var needed, count, resume uint32
	var buf []byte
	for {
		var p *byte
		if len(buf) > 0 {
			p = &buf[0]
		}
		err = windows.EnumServicesStatusEx(scm, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32,
			windows.SERVICE_STATE_ALL, p, uint32(len(buf)), &needed, &count, &resume, nil)
		if err == nil {
			break
		}
		if err != windows.ERROR_MORE_DATA {
			return nil, err
		}
		buf = make([]byte, needed)
		resume = 0
	}
	if count == 0 {
		return nil, nil
	}

	entries := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), count)
	services := make([]Service, 0, count)
	for _, e := range entries {
		name := windows.UTF16PtrToString(e.ServiceName)
		services = append(services, Service{
			Name:        name,
			DisplayName: windows.UTF16PtrToString(e.DisplayName),
			State:       serviceStates[e.ServiceStatusProcess.CurrentState],
			StartType:   serviceStartType(scm, name),
			PID:         e.ServiceStatusProcess.ProcessId,
		})
	}
	return services, nil
}

func serviceStartType(scm windows.Handle, name string) string {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return ""
	}
	h, err := windows.OpenService(scm, namePtr, windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return ""
	}
	defer windows.CloseServiceHandle(h)

	var needed uint32
	_ = windows.QueryServiceConfig(h, nil, 0, &needed)
	if needed == 0 {
		return ""
	}
	buf := make([]byte, needed)
	cfg := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buf[0]))
	if err := windows.QueryServiceConfig(h, cfg, needed, &needed); err != nil {
		return ""
	}
	return startTypes[cfg.StartType]
}
