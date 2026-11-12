package darwin

/*
#include <objc/runtime.h>
#include <objc/message.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdint.h>
#include <string.h>

static BOOL lifecycle_true(id object, SEL selector) { return YES; }
static void lifecycle_noop(id object, SEL selector) {}
static int lifecycle_discards;
static void lifecycle_discard(id object, SEL selector) { lifecycle_discards++; }
static id lifecycle_context(id object, SEL selector) {
    static id context;
    if (!context) {
        Class contextClass = objc_allocateClassPair(objc_getClass("NSObject"), "VivaLifecycleProbeContext", 0);
        class_addMethod(contextClass, sel_registerName("discardMarkedText"), (IMP)lifecycle_discard, "v@:");
        class_addMethod(contextClass, sel_registerName("deactivate"), (IMP)lifecycle_noop, "v@:");
        class_addMethod(contextClass, sel_registerName("activate"), (IMP)lifecycle_noop, "v@:");
        class_addMethod(contextClass, sel_registerName("invalidateCharacterCoordinates"), (IMP)lifecycle_noop, "v@:");
        objc_registerClassPair(contextClass);
        context = ((id (*)(id, SEL))objc_msgSend)((id)contextClass, sel_registerName("alloc"));
        context = ((id (*)(id, SEL))objc_msgSend)(context, sel_registerName("init"));
    }
    return context;
}
static void lifecycle_write(id object, const char *name, const void *value, size_t size) {
    Ivar field = class_getInstanceVariable(object_getClass(object), name);
    memcpy((char *)object + ivar_getOffset(field), value, size);
}
static void *lifecycle_create(uintptr_t handle, int terminal) {
    Class probe = objc_getClass("VivaLifecycleProbeView");
    if (!probe) {
        probe = objc_allocateClassPair(objc_getClass("VivaMetalView"), "VivaLifecycleProbeView", 0);
        class_addMethod(probe, sel_registerName("rebuildTextTexture"), (IMP)lifecycle_true, "B@:");
        class_addMethod(probe, sel_registerName("inputContext"), (IMP)lifecycle_context, "@@:");
        class_addMethod(probe, sel_registerName("restartCaretBlink"), (IMP)lifecycle_noop, "v@:");
        class_addMethod(probe, sel_registerName("stopCaretBlink"), (IMP)lifecycle_noop, "v@:");
        class_addMethod(probe, sel_registerName("render"), (IMP)lifecycle_noop, "v@:");
        objc_registerClassPair(probe);
    }
    id object = ((id (*)(id, SEL))objc_msgSend)((id)probe, sel_registerName("alloc"));
    object = ((id (*)(id, SEL))objc_msgSend)(object, sel_registerName("init"));
    BOOL enabled = YES;
    BOOL terminalEnabled = terminal != 0;
    lifecycle_write(object, "_textInputEnabled", &enabled, sizeof(enabled));
    lifecycle_write(object, "_dynamicTextInput", &enabled, sizeof(enabled));
    lifecycle_write(object, "_hasMarkedText", &enabled, sizeof(enabled));
    lifecycle_write(object, "_tentativeComposition", &enabled, sizeof(enabled));
    lifecycle_write(object, "_textTerminal", &terminalEnabled, sizeof(terminalEnabled));
    lifecycle_write(object, "_callbackHandle", &handle, sizeof(handle));
    CFStringRef value = CFStringCreateWithCString(NULL, "abcx", kCFStringEncodingUTF8);
    object_setIvar(object, class_getInstanceVariable(probe, "_text"), (id)value);
    struct { unsigned long start, length; } selection = {4, 0}, marked = {3, 1};
    lifecycle_write(object, "_selectedRange", &selection, sizeof(selection));
    lifecycle_write(object, "_markedRange", &marked, sizeof(marked));
    return object;
}
static const char *lifecycle_text(void *pointer) {
    id object = (id)pointer;
    id value = object_getIvar(object, class_getInstanceVariable(object_getClass(object), "_text"));
    return ((const char *(*)(id, SEL))objc_msgSend)(value, sel_registerName("UTF8String"));
}
static void lifecycle_destroy(void *pointer) {
    uintptr_t handle = 0;
    lifecycle_write((id)pointer, "_callbackHandle", &handle, sizeof(handle));
    ((void (*)(id, SEL))objc_msgSend)((id)pointer, sel_registerName("release"));
}
static void lifecycle_reset_discards(void) { lifecycle_discards = 0; }
static int lifecycle_discard_count(void) { return lifecycle_discards; }
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

func compositionLifecycleView(state *runState) (unsafe.Pointer, func()) {
	handle := cgo.NewHandle(state)
	terminal := 0
	if state.textInput.Terminal {
		terminal = 1
	}
	view := C.lifecycle_create(C.uintptr_t(handle), C.int(terminal))
	return view, func() {
		C.lifecycle_destroy(view)
		handle.Delete()
	}
}

func compositionLifecycleText(view unsafe.Pointer) string {
	return C.GoString(C.lifecycle_text(view))
}

func resetCompositionLifecycleDiscards() {
	C.lifecycle_reset_discards()
}

func compositionLifecycleDiscards() int {
	return int(C.lifecycle_discard_count())
}
