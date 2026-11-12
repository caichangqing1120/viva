#include "../native.m"
#include "../main_menu_darwin.m"
#include <assert.h>
#include <limits.h>
#include <stdint.h>

// The native half of the E3B provenance handoff. The view reaches the real
// NSTextInputClient entry points, and this callback records what the adapter
// hands to Go: event kind, composition phase, raw UTF-16 replacement, and text.
// Go passes its accepted CompositionPhase numbers in with -D, so the static
// assertions bind the native enum to text.CompositionPhase.

typedef struct {
    int kind;
    int phase;
    int command;
    int hasReplacementUTF16;
    int64_t replacementStart;
    int64_t replacementLength;
    char text[64];
} recorded_text_event;

static recorded_text_event records[32];
static int recordCount;
static int lostCount;
static int lostTotal;

static void reset_records(void) {
    memset(records, 0, sizeof(records));
    recordCount = 0;
    lostCount = 0;
}

static recorded_text_event *last_record(void) {
    assert(recordCount > 0);
    return &records[recordCount - 1];
}

static void expect_record(int kind, int phase, int hasReplacementUTF16,
                          int64_t replacementStart, int64_t replacementLength,
                          const char *text) {
    recorded_text_event *event = last_record();
    assert(event->kind == kind);
    assert(event->phase == phase);
    assert(event->hasReplacementUTF16 == hasReplacementUTF16);
    if (hasReplacementUTF16) {
        assert(event->replacementStart == replacementStart);
        assert(event->replacementLength == replacementLength);
    }
    assert(strcmp(event->text, text) == 0);
}

void vivaPointerEvent(uintptr_t handle, void *view, int kind, int button, int modifiers, int clickCount, double x, double y, double screenX, double screenY) {}
void vivaViewResized(uintptr_t handle, void *view, double width, double height) {}
void vivaRedrawRequested(uintptr_t handle, void *view) {}
void vivaKeyEvent(uintptr_t handle, void *view, int key, int modifiers, int repeat, const unsigned char *utf8, size_t utf8Length) {}
void vivaRawKeyEvent(uintptr_t handle, void *view, int key, int modifiers, int flags, unsigned int code, const unsigned char *utf8, size_t utf8Length, const unsigned char *typed, size_t typedLength) {}
void vivaFocusLost(uintptr_t handle, void *view) {}
int vivaTerminalCells(uint32_t character) { return 1; }
int vivaAccessibilityAction(uintptr_t handle, void *view, uint64_t node, int action, double number, const unsigned char *utf8, size_t utf8Length, int start, int end) { return 0; }
viva_text_line *vivaShapedLine(const unsigned char *utf8, size_t length, double size, double weight) { return viva_text_line_create(utf8, length, size, weight); }
viva_text_line *vivaShapedLineStyle(const unsigned char *utf8, size_t length, double size, double weight, int italic) { return viva_text_line_create_style(utf8, length, size, weight, italic); }
int vivaShortcutEvent(uintptr_t handle, void *view, int key, int modifiers, int repeat, const unsigned char *utf8, size_t utf8Length) { return 0; }
void vivaOpenURL(const char *url) {}
int vivaApplicationQuitRequested(void) { return 0; }
int vivaApplicationReopenRequested(void) { return 0; }
int vivaFileDropEvent(uintptr_t handle, void *view, int phase, double x, double y, const char *paths, size_t length) { return 0; }
void vivaScrollEvent(uintptr_t handle, void *view, double deltaX, double deltaY, double x, double y, int modifiers, int precise) {}
void vivaCloseRequested(uintptr_t handle, void *view) {}
void vivaWindowClosed(uintptr_t handle) {}
void vivaFrameChanged(uintptr_t handle, void *view, double x, double y, double width, double height) {}
void vivaWindowMinimized(uintptr_t handle, void *view, int minimized) {}
void vivaWindowOccluded(uintptr_t handle, void *view, int visible) {}
void vivaWindowFullscreen(uintptr_t handle, void *view, int fullscreen) {}
int vivaWindowReopenRequested(uintptr_t handle, void *view) { return 0; }
uint16_t vivaRectangleLayer(uintptr_t handle, size_t index) { return 0; }
int vivaPressAt(uintptr_t handle, double x, double y) { return 0; }

void vivaTextInput(uintptr_t handle, void *view, int kind,
                   const unsigned char *utf8, size_t utf8Length,
                   int64_t selectedStart, int64_t selectedLength,
                   int64_t replacementStart, int64_t replacementLength,
                   int command, int compositionPhase,
                   int64_t replacementUTF16Start, int64_t replacementUTF16Length,
                   int hasReplacementUTF16) {
    (void)handle;
    (void)view;
    (void)selectedStart;
    (void)selectedLength;
    assert(recordCount < (int)(sizeof(records) / sizeof(records[0])));
    recorded_text_event *event = &records[recordCount++];
    memset(event, 0, sizeof(*event));
    event->kind = kind;
    event->phase = compositionPhase;
    event->command = command;
    event->hasReplacementUTF16 = hasReplacementUTF16;
    event->replacementStart = replacementUTF16Start;
    event->replacementLength = replacementUTF16Length;
    if (utf8Length > 0) {
        size_t copy = MIN(utf8Length, sizeof(event->text) - 1);
        memcpy(event->text, utf8, copy);
    }
}

void vivaTextCompositionLost(uintptr_t handle, void *view, int reason) {
    (void)handle;
    (void)view;
    assert(reason == 1);
    lostCount++;
    lostTotal++;
}

_Static_assert(VIVA_COMPOSITION_NONE == GO_COMPOSITION_NONE, "composition none");
_Static_assert(VIVA_COMPOSITION_UPDATE == GO_COMPOSITION_UPDATE, "composition update");
_Static_assert(VIVA_COMPOSITION_RESULT == GO_COMPOSITION_RESULT, "composition result");
_Static_assert(VIVA_COMPOSITION_COMMIT == GO_COMPOSITION_COMMIT, "composition commit");
_Static_assert(VIVA_COMPOSITION_CANCEL == GO_COMPOSITION_CANCEL, "composition cancel");
_Static_assert(VIVA_COMPOSITION_EMPTY_COMMIT == GO_COMPOSITION_EMPTY_COMMIT, "composition empty commit");

@interface ProvenanceView : VivaMetalView
- (void)reset;
- (void)setMarked:(BOOL)value;
- (void)setTentative:(BOOL)value;
- (void)setText:(NSString *)value;
@end

@implementation ProvenanceView
- (void)reset {
    [self stopCaretBlink];
    [_text release];
    _text = [@"" retain];
    _textInputEnabled = YES;
    _dynamicTextInput = YES;
    _callbackHandle = 1;
    _hasMarkedText = NO;
    _tentativeComposition = NO;
    _markedRange = NSMakeRange(0, 0);
    _selectedRange = NSMakeRange(0, 0);
}
- (NSTextInputContext *)inputContext { return nil; }
- (BOOL)rebuildTextTexture { return YES; }
- (void)setMarked:(BOOL)value { _hasMarkedText = value; }
- (void)setTentative:(BOOL)value { _tentativeComposition = value; }
- (void)setText:(NSString *)value {
    [_text release];
    _text = [value copy];
    _selectedRange = NSMakeRange(0, 0);
}
@end

int main(void) {
    @autoreleasepool {
        ProvenanceView *view = [[ProvenanceView alloc] initWithFrame:NSMakeRect(0, 0, 200, 30)];

        // An input method starts a composition: update, no replacement range.
        reset_records();
        [view reset];
        [view setMarkedText:@"ni"
              selectedRange:NSMakeRange(2, 0)
            replacementRange:NSMakeRange(NSNotFound, 0)];
        assert(recordCount == 1);
        expect_record(VIVA_TEXT_SET_MARKED, VIVA_COMPOSITION_UPDATE, 0, 0, 0, "ni");
        // The synchronous Go update marks the native editor as composing.
        [view setMarked:YES];

        // A later update keeps the raw replacement facts, even when they are
        // out of range: conversion and fallback belong to the accepted E1 path.
        [view setMarkedText:@"你"
              selectedRange:NSMakeRange(1, 0)
            replacementRange:NSMakeRange(99, 99)];
        assert(recordCount == 2);
        expect_record(VIVA_TEXT_SET_MARKED, VIVA_COMPOSITION_UPDATE, 1, 99, 99, "你");

        // A committed result: insert with the raw caller range untouched.
        [view insertText:@"r" replacementRange:NSMakeRange(5, 1)];
        assert(recordCount == 3);
        expect_record(VIVA_TEXT_INSERT, VIVA_COMPOSITION_RESULT, 1, 5, 1, "r");

        // An empty result is an explicit empty commit, not an ordinary insert.
        [view insertText:@"" replacementRange:NSMakeRange(NSNotFound, 0)];
        assert(recordCount == 4);
        expect_record(VIVA_TEXT_INSERT, VIVA_COMPOSITION_EMPTY_COMMIT, 0, 0, 0, "");

        // The empty commit can name a range to remove (an input method deleting
        // a selected span), and that range is raw UTF-16.
        [view insertText:@"" replacementRange:NSMakeRange(2, 3)];
        assert(recordCount == 5);
        expect_record(VIVA_TEXT_INSERT, VIVA_COMPOSITION_EMPTY_COMMIT, 1, 2, 3, "");

        // An empty marked string cancels a live composition.
        [view setMarkedText:@""
              selectedRange:NSMakeRange(0, 0)
            replacementRange:NSMakeRange(NSNotFound, 0)];
        assert(recordCount == 6);
        expect_record(VIVA_TEXT_SET_MARKED, VIVA_COMPOSITION_CANCEL, 0, 0, 0, "");

        // unmarkText commits the live marked text without changing it.
        [view setMarkedText:@"x" selectedRange:NSMakeRange(1, 0) replacementRange:NSMakeRange(NSNotFound, 0)];
        [view setMarked:YES];
        NSUInteger beforeCommit = recordCount;
        [view unmarkText];
        assert(recordCount == beforeCommit + 1);
        expect_record(VIVA_TEXT_UNMARK, VIVA_COMPOSITION_COMMIT, 0, 0, 0, "");

        // An extreme but present range is clamped to a positive int64 instead
        // of wrapping negative; E1 still sees a huge relative length.
        [view setMarked:YES];
        [view insertText:@"z" replacementRange:NSMakeRange(0, NSNotFound)];
        expect_record(VIVA_TEXT_INSERT, VIVA_COMPOSITION_RESULT, 1, 0, INT64_MAX, "z");

        // No live marking: ordinary typing and unmark stay legacy/untagged.
        [view setMarked:NO];
        NSUInteger legacy = recordCount;
        [view insertText:@"p" replacementRange:NSMakeRange(NSNotFound, 0)];
        expect_record(VIVA_TEXT_INSERT, VIVA_COMPOSITION_NONE, 0, 0, 0, "p");
        [view unmarkText];
        expect_record(VIVA_TEXT_UNMARK, VIVA_COMPOSITION_NONE, 0, 0, 0, "");
        [view setMarkedText:@""
              selectedRange:NSMakeRange(0, 0)
            replacementRange:NSMakeRange(NSNotFound, 0)];
        expect_record(VIVA_TEXT_SET_MARKED, VIVA_COMPOSITION_NONE, 0, 0, 0, "");
        assert(recordCount == legacy + 3);

        // Losing the editor/discarding marked text must not fabricate a phase
        // event. The native Clear is state-only; no callback may arrive after.
        [view setMarked:YES];
        lostCount = 0;
        NSUInteger beforeClear = recordCount;
        [view shutdownTextInput];
        assert(![view hasMarkedText]);
        assert(lostCount == 0);
        assert(recordCount == beforeClear);

        [view reset];
        [view setMarked:YES];
        lostCount = 0;
        beforeClear = recordCount;
        assert([view updateTextInput:(viva_text_input_config){0}] == 0);
        assert(![view hasMarkedText]);
        assert(lostCount == 0);
        assert(recordCount == beforeClear);

        // E3C: a live owned tentative reports exactly one lost Cancel when the
        // native editor shuts down, and a second shutdown is idempotent.
        [view reset];
        [view setTentative:YES];
        [view setMarked:YES];
        lostCount = 0;
        NSUInteger beforeLost = recordCount;
        [view shutdownTextInput];
        assert(![view hasMarkedText]);
        assert(lostCount == 1);
        assert(recordCount == beforeLost);
        [view shutdownTextInput];
        assert(lostCount == 1);

        // Re-enabling and starting a fresh Update still works after the loss.
        viva_text_input_config config = {0};
        config.enabled = 1;
        config.tentative_composition = 1;
        config.width = 100;
        config.height = 20;
        config.font_size = 12;
        config.utf8 = (const unsigned char *)"abc";
        config.utf8_length = 3;
        assert([view updateTextInput:config] == 0);
        lostCount = 0;
        NSUInteger beforeUpdate = recordCount;
        [view setMarkedText:@"n"
              selectedRange:NSMakeRange(1, 0)
            replacementRange:NSMakeRange(NSNotFound, 0)];
        expect_record(VIVA_TEXT_SET_MARKED, VIVA_COMPOSITION_UPDATE, 0, 0, 0, "n");
        assert(lostCount == 0);
        assert(recordCount == beforeUpdate + 1);

        // View focus loss ends the owned composition without a text event.
        [view reset];
        [view setTentative:YES];
        [view setText:@"abc"];
        [view setMarked:YES];
        lostCount = 0;
        beforeLost = recordCount;
        [view resignFirstResponder];
        assert(![view hasMarkedText]);
        assert(lostCount == 1);
        assert(recordCount == beforeLost);

        // A selection drag after discardMarkedText reports Cancel first, then
        // the ordinary untagged selection transition.
        [view reset];
        [view setTentative:YES];
        [view setText:@"abc"];
        [view setMarked:YES];
        lostCount = 0;
        beforeLost = recordCount;
        [view selectTextRange:NSMakeRange(1, 1)];
        assert(lostCount == 1);
        assert(recordCount == beforeLost + 1);
        expect_record(VIVA_TEXT_SET_SELECTION, VIVA_COMPOSITION_NONE, 0, 0, 0, "");

        // A valid Go state push that keeps the mark leaves the preedit alone.
        [view reset];
        [view setTentative:YES];
        [view setText:@"abc"];
        [view setMarked:YES];
        lostCount = 0;
        beforeLost = recordCount;
        assert([view updateText:(const unsigned char *)"abc"
                          length:3
                   selectedRange:NSMakeRange(1, 0)
                     markedRange:NSMakeRange(0, 1)
                   hasMarkedText:YES
            tentativeComposition:YES
               endsComposition:NO] == 0);
        assert([view hasMarkedText]);
        assert(lostCount == 0);
        assert(recordCount == beforeLost);

        // A Go-mirrored state push that drops the mark without a terminal
        // event is a lost composition: exactly one Cancel, and no text event.
        [view reset];
        [view setTentative:YES];
        [view setText:@"abc"];
        [view setMarked:YES];
        lostCount = 0;
        beforeLost = recordCount;
        assert([view updateText:(const unsigned char *)"abc"
                          length:3
                   selectedRange:NSMakeRange(1, 0)
                     markedRange:NSMakeRange(0, 0)
                   hasMarkedText:NO
            tentativeComposition:YES
               endsComposition:NO] == 0);
        assert(![view hasMarkedText]);
        assert(lostCount == 1);
        assert(recordCount == beforeLost);
        // The mark is gone, so repeating the same push is not a second loss.
        assert([view updateText:(const unsigned char *)"abc"
                          length:3
                   selectedRange:NSMakeRange(1, 0)
                     markedRange:NSMakeRange(0, 0)
                   hasMarkedText:NO
            tentativeComposition:YES
               endsComposition:NO] == 0);
        assert(lostCount == 1);
        assert(recordCount == beforeLost);

        // A Go-originated terminal Cancel/Commit pushes the same cleared mark
        // with endsComposition:YES. Go already reported that terminal event, so
        // native must not fabricate a second lost callback.
        [view reset];
        [view setTentative:YES];
        [view setText:@"abc"];
        [view setMarked:YES];
        lostCount = 0;
        beforeLost = recordCount;
        assert([view updateText:(const unsigned char *)"abc"
                          length:3
                   selectedRange:NSMakeRange(1, 0)
                     markedRange:NSMakeRange(0, 0)
                   hasMarkedText:NO
            tentativeComposition:YES
               endsComposition:YES] == 0);
        assert(![view hasMarkedText]);
        assert(lostCount == 0);
        assert(recordCount == beforeLost);

        // A returned passive/disabled snapshot is legacy discard even when the
        // terminal flag is clear: no tagged event may be fabricated.
        [view reset];
        [view setTentative:YES];
        [view setText:@"abc"];
        [view setMarked:YES];
        lostCount = 0;
        beforeLost = recordCount;
        assert([view updateText:(const unsigned char *)"abc"
                          length:3
                   selectedRange:NSMakeRange(1, 0)
                     markedRange:NSMakeRange(0, 0)
                   hasMarkedText:NO
            tentativeComposition:NO
               endsComposition:NO] == 0);
        assert(![view hasMarkedText]);
        assert(lostCount == 0);
        assert(recordCount == beforeLost);

        // Replacing the native editor while marked releases the old surface
        // without a second tag (the Go bridge reports that Cancel before the
        // handler swap), and a new mark is accepted cleanly.
        [view reset];
        [view setTentative:YES];
        [view setText:@"abc"];
        [view setMarked:YES];
        lostCount = 0;
        beforeLost = recordCount;
        config.utf8 = (const unsigned char *)"xy";
        config.utf8_length = 2;
        config.has_marked_text = 0;
        config.marked_start = 0;
        config.marked_length = 0;
        assert([view updateTextInput:config] == 0);
        assert(![view hasMarkedText]);
        assert(lostCount == 0);
        assert(recordCount == beforeLost);
        config.has_marked_text = 1;
        config.marked_start = 0;
        config.marked_length = 2;
        assert([view updateTextInput:config] == 0);
        assert([view hasMarkedText]);
        assert(lostCount == 0);

        // A passive snapshot keeps legacy discard: no tagged lost event.
        [view reset];
        [view setMarked:YES];
        config.tentative_composition = 0;
        config.has_marked_text = 1;
        assert([view updateTextInput:config] == 0);
        assert([view hasMarkedText]);
        lostCount = 0;
        beforeLost = recordCount;
        [view shutdownTextInput];
        assert(lostCount == 0);
        assert(recordCount == beforeLost);

        // An active Clear disables the capability, so it is legacy discard: no
        // fabricated Cancel/Commit/Result.
        [view reset];
        [view setTentative:YES];
        [view setMarked:YES];
        lostCount = 0;
        beforeLost = recordCount;
        assert([view updateTextInput:(viva_text_input_config){0}] == 0);
        assert(![view hasMarkedText]);
        assert(lostCount == 0);
        assert(recordCount == beforeLost);

        printf("native text provenance passed; records=%d lost=%d\n", recordCount, lostTotal);
        [view release];
    }
    return 0;
}
