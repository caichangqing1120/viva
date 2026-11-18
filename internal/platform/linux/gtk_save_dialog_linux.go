//go:build linux && vivagtk

package linux

import (
	"fmt"
	"strings"
	"syscall"
)

// saveFileDialog keeps the fallback dialog transient for the active Wayland
// window. A separate, unparented helper cannot return compositor focus reliably.
func (a *gtkApplication) saveFileDialog(title, name string, filters []portalFilter) (string, bool, error) {
	if a.mainThread != syscall.Gettid() || !a.wayland || a.stopping || a.active == nil || a.active.closed || a.active.destroying || strings.Contains(name, "/") {
		return "", false, errServiceMissing
	}
	if a.saveDialogOwner != nil {
		return "", false, fmt.Errorf("linux: a save file dialog is already open")
	}
	if strings.ContainsRune(title, 0) {
		return "", false, fmt.Errorf("linux: save file title contains NUL")
	}
	if title == "" {
		title = "Save As"
	}
	var api struct {
		newDialog  func(title *byte, parent uintptr, action int32, accept, cancel *byte) uintptr
		run        func(uintptr) int32
		destroy    func(uintptr)
		hide       func(uintptr)
		modal      func(uintptr, int32)
		overwrite  func(uintptr, int32)
		setName    func(uintptr, *byte)
		filename   func(uintptr) uintptr
		newFilter  func() uintptr
		filterName func(uintptr, *byte)
		pattern    func(uintptr, *byte)
		addFilter  func(dialog, filter uintptr)
	}
	for _, symbol := range []struct {
		function any
		name     string
	}{
		{&api.newDialog, "gtk_file_chooser_native_new"}, {&api.run, "gtk_native_dialog_run"},
		{&api.destroy, "gtk_native_dialog_destroy"}, {&api.hide, "gtk_native_dialog_hide"}, {&api.modal, "gtk_native_dialog_set_modal"},
		{&api.overwrite, "gtk_file_chooser_set_do_overwrite_confirmation"}, {&api.setName, "gtk_file_chooser_set_current_name"},
		{&api.filename, "gtk_file_chooser_get_filename"}, {&api.newFilter, "gtk_file_filter_new"},
		{&api.filterName, "gtk_file_filter_set_name"}, {&api.pattern, "gtk_file_filter_add_pattern"},
		{&api.addFilter, "gtk_file_chooser_add_filter"},
	} {
		if !a.g.lookup(symbol.function, symbol.name) {
			return "", false, errServiceMissing
		}
	}

	const saveAction, acceptResponse = 1, -3
	parent := a.active
	dialog := api.newDialog(&cString(title)[0], parent.toplevel, saveAction, nil, nil)
	if dialog == 0 {
		return "", false, fmt.Errorf("linux: could not create a GTK save file dialog")
	}
	a.saveDialogOwner = parent
	a.dismissSaveDialog = func() { api.hide(dialog) }
	defer func() {
		api.destroy(dialog)
		a.g.objectUnref(dialog)
		a.dismissSaveDialog = nil
		a.saveDialogOwner = nil
	}()
	api.modal(dialog, 1)
	api.overwrite(dialog, 1)
	if name != "" {
		api.setName(dialog, &cString(name)[0])
	}
	for _, filter := range filters {
		ptr := api.newFilter()
		api.filterName(ptr, &cString(filter.name)[0])
		for _, pattern := range filter.patterns {
			api.pattern(ptr, &cString(pattern)[0])
		}
		api.addFilter(dialog, ptr) // The chooser takes ownership.
	}
	if api.run(dialog) != acceptResponse || parent.closed || a.stopping {
		return "", true, nil
	}
	path := api.filename(dialog)
	if path == 0 {
		return "", false, fmt.Errorf("linux: GTK save file dialog returned no local path")
	}
	defer a.g.gFree(path)
	return goString(path), false, nil
}
