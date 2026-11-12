#include <cstddef>
#include <cstdint>
#include <cassert>
#include <stdexcept>
#include <utility>
#include <map>
#include <optional>
#include <memory>
#include <algorithm>
#include <iterator>
#include <chrono>
#include <forward_list>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <string_view>
#include <vector>

#include "Sci_Position.h"
#include "ScintillaTypes.h"
#include "ILoader.h"
#include "ILexer.h"
#include "Debugging.h"
#include "CharacterType.h"
#include "CharacterCategoryMap.h"
#include "Position.h"
#include "SplitVector.h"
#include "Partitioning.h"
#include "SparseVector.h"
#include "RunStyles.h"
#include "ChangeHistory.h"
#include "CellBuffer.h"
#include "PerLine.h"
#include "CharClassify.h"
#include "Decoration.h"
#include "CaseFolder.h"
#include "Document.h"
#include "UniConversion.h"

using namespace Scintilla;
using namespace Scintilla::Internal;

struct Group {
    Document &doc;
    explicit Group(Document &d) : doc(d) { doc.BeginUndoAction(); }
    ~Group() { doc.EndUndoAction(); }
};

static std::string Text(Document &doc) {
    const Sci::Position n = doc.Length();
    return std::string(doc.RangePointer(0, n), static_cast<size_t>(n));
}

static void Init(Document &doc, const std::string &initial) {
    doc.InsertString(0, initial);
    doc.DeleteUndoHistory();
}

static void State(const char *tag, Document &doc) {
    std::printf("%s text=%s len=%lld cur=%d act=%d save=%d undo=%d redo=%d\n", tag,
        Text(doc).c_str(), static_cast<long long>(doc.Length()), doc.UndoCurrent(),
        doc.UndoActions(), doc.UndoSavePoint(), doc.CanUndo() ? 1 : 0, doc.CanRedo() ? 1 : 0);
}

static void Undo(Document &doc, const char *tag) {
    const int before = doc.UndoCurrent();
    doc.Undo();
    std::printf("%s text=%s cur=%d act=%d delta=%d undo=%d redo=%d\n", tag,
        Text(doc).c_str(), doc.UndoCurrent(), doc.UndoActions(),
        before - doc.UndoCurrent(), doc.CanUndo() ? 1 : 0, doc.CanRedo() ? 1 : 0);
}

// Exact Editor::InsertCharacter empty-selection branch (Editor.cxx:2113-2122),
// not an Editor execution. The caller controls the one UndoGroup per call.
static void Scalar(Document &doc, Sci::Position &pos, std::string_view sv, bool overwrite) {
    Group group(doc);
    if (overwrite && pos < doc.Length() && !doc.IsPositionInLineEnd(pos))
        doc.DelChar(pos);
    doc.InsertString(pos, sv);
    pos += static_cast<Sci::Position>(sv.size());
}

// Exact nonempty-selection first-call branch (Editor.cxx:2111-2122): delete the
// selected byte range, then insert the first scalar in the same group.
static void SelectedFirst(Document &doc, Sci::Position &pos, Sci::Position selLen, std::string_view sv) {
    Group group(doc);
    doc.DeleteChars(pos, selLen);
    doc.InsertString(pos, sv);
    pos += static_cast<Sci::Position>(sv.size());
}

static void PlainInsert(Document &doc, Sci::Position &pos, std::string_view sv) {
    doc.InsertString(pos, sv);
    pos += static_cast<Sci::Position>(sv.size());
}

// InsertCharacter with empty selection and the current inOverstrike flag:
// one UndoGroup per scalar in overwrite, no group in insertion mode.
static void ScalarAuto(Document &doc, Sci::Position &pos, std::string_view sv, bool overwrite) {
    if (overwrite) {
        Group group(doc);
        if (pos < doc.Length() && !doc.IsPositionInLineEnd(pos)) doc.DelChar(pos);
        doc.InsertString(pos, sv);
    } else {
        doc.InsertString(pos, sv);
    }
    pos += static_cast<Sci::Position>(sv.size());
}

// ClearBeforeTentativeStart for a nonempty selection (Editor.cxx:2192-2202):
// one ordinary group deletes the selection, then TentativeStart records the base.
static void FirstPreeditOverSelection(Document &doc, Sci::Position &pos, Sci::Position selLen) {
    { Group group(doc); doc.DeleteChars(pos, selLen); }
    doc.TentativeStart();
}

namespace Scintilla::Internal {
namespace Platform {
void Assert(const char *, const char *, int) noexcept { std::abort(); }
void DebugPrintf(const char *, ...) noexcept {}
}
}

int main() {
    std::setvbuf(stdout, nullptr, _IONBF, 0);
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 0;
        State("ascii0-before", d); Scalar(d, p, "X", true); State("ascii0-after", d); Undo(d, "ascii0-undo1");
    }
    {
        Document d(DocumentOption::Default); Init(d, "a\xC3\xA9" "b"); Sci::Position p = 0;
        Scalar(d, p, "X", true); State("bmp-caret0-after", d); Undo(d, "bmp-caret0-undo1");
    }
    {
        Document d(DocumentOption::Default); Init(d, "a\xF0\x9D\x84\x9E" "b"); Sci::Position p = 0;
        Scalar(d, p, "X", true); State("astral-caret0-after", d); Undo(d, "astral-caret0-undo1");
    }
    {
        Document d(DocumentOption::Default); Init(d, "a\xC3\xA9" "b"); Sci::Position p = 1;
        Scalar(d, p, "X", true); State("bmp-caret1-after", d); Undo(d, "bmp-caret1-undo1");
    }
    {
        Document d(DocumentOption::Default); Init(d, "a\xF0\x9D\x84\x9E" "b"); Sci::Position p = 1;
        Scalar(d, p, "X", true); State("astral-caret1-after", d); Undo(d, "astral-caret1-undo1");
    }
    {
        Document d(DocumentOption::Default); Init(d, "a\xCC\x81" "b"); Sci::Position p = 1;
        Scalar(d, p, "X", true); State("combining-caret1-after", d); Undo(d, "combining-caret1-undo1");
    }
    {
        Document d(DocumentOption::Default); Init(d, "a\xCC\x81" "b"); Sci::Position p = 0;
        Scalar(d, p, "X", true); State("combining-caret0-after", d); Undo(d, "combining-caret0-undo1");
    }
    {
        Document d(DocumentOption::Default); Init(d, "ab\ncd"); Sci::Position p = 2;
        Scalar(d, p, "X", true); State("lf-eol-after", d); Undo(d, "lf-eol-undo1");
    }
    {
        Document d(DocumentOption::Default); Init(d, "ab\r\ncd"); Sci::Position p = 2;
        Scalar(d, p, "X", true); State("crlf-eol-after", d); Undo(d, "crlf-eol-undo1");
    }
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 3;
        Scalar(d, p, "X", true); State("eof-after", d); Undo(d, "eof-undo1");
    }
    {
        Document d(DocumentOption::Default); Init(d, "abcdef"); Sci::Position p = 1;
        SelectedFirst(d, p, 2, "X"); Scalar(d, p, "Y", true); State("selection-bc-XY-after", d);
        Undo(d, "selection-bc-XY-undo1"); Undo(d, "selection-bc-XY-undo2");
    }
    {
        Document d(DocumentOption::Default); Init(d, "ab\ncd"); Sci::Position p = 0;
        SelectedFirst(d, p, 2, "X"); Scalar(d, p, "Y", true); State("selection-ab-eol-after", d);
    }
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 0;
        { Group group(d); if (p < d.Length() && !d.IsPositionInLineEnd(p)) d.DelChar(p); d.InsertString(p, "XY"); }
        State("raw-multiscalar-after", d); Undo(d, "raw-multiscalar-undo1");
    }
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 1;
        d.TentativeStart(); Scalar(d, p, "x", true); State("preedit-x", d);
        d.TentativeUndo(); State("preedit-x-cancel", d);
        p = 1; d.TentativeStart(); Scalar(d, p, "y", true); Scalar(d, p, "z", true); State("preedit-yz", d);
        Undo(d, "preedit-yz-undo1"); Undo(d, "preedit-yz-undo2"); d.TentativeUndo(); State("preedit-yz-cancel", d);
    }
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 1;
        d.TentativeStart(); Scalar(d, p, "x", true); Scalar(d, p, "y", true); State("commit-before", d);
        d.TentativeCommit(); State("commit-after", d); Undo(d, "commit-undo1"); Undo(d, "commit-undo2");
    }
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 1;
        d.TentativeStart(); Scalar(d, p, "x", true); State("result-preedit-x", d);
        d.TentativeUndo(); p = 1; Scalar(d, p, "R", true); Scalar(d, p, "S", true); State("result-RS-after-rollback", d);
        Undo(d, "result-RS-undo1"); Undo(d, "result-RS-undo2");
    }
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 1;
        SelectedFirst(d, p, 1, "r"); State("win32-result-selected-r", d);
        d.TentativeStart(); Scalar(d, p, "x", true); State("win32-result-preedit-x", d);
        d.TentativeUndo(); p = 2; Scalar(d, p, "y", true); State("win32-next-result-y", d);
    }
    {
        Document d(DocumentOption::Default); Init(d, "abcdef"); Sci::Position p = 1;
        FirstPreeditOverSelection(d, p, 2); State("firstpreedit-sel-base", d);
        ScalarAuto(d, p, "x", true); State("firstpreedit-sel-x", d);
        d.TentativeUndo(); State("firstpreedit-sel-cancel-x", d);
        d.TentativeCommit();
        Undo(d, "firstpreedit-sel-undo-base");
    }
    {
        Document d(DocumentOption::Default); Init(d, "abcdef"); Sci::Position p = 1;
        FirstPreeditOverSelection(d, p, 2);
        ScalarAuto(d, p, "x", true);
        d.TentativeUndo(); p = 1; d.TentativeStart();
        ScalarAuto(d, p, "y", true); ScalarAuto(d, p, "z", true);
        State("firstpreedit-sel-update-yz", d);
        d.TentativeUndo(); d.TentativeCommit(); State("firstpreedit-sel-update-cancel", d);
        Undo(d, "firstpreedit-sel-update-undo-base");
    }
    {
        Document d(DocumentOption::Default); Init(d, "abcdef"); Sci::Position p = 1;
        FirstPreeditOverSelection(d, p, 2); ScalarAuto(d, p, "x", true);
        d.TentativeUndo(); p = 1;
        ScalarAuto(d, p, "R", true); ScalarAuto(d, p, "S", true);
        State("firstpreedit-sel-result-RS", d);
        Undo(d, "firstpreedit-sel-result-undo1"); Undo(d, "firstpreedit-sel-result-undo2");
        Undo(d, "firstpreedit-sel-result-undo3");
    }
    {
        Document d(DocumentOption::Default); Init(d, "abcdef"); Sci::Position p = 1;
        d.TentativeStart(); ScalarAuto(d, p, "x", true); d.TentativeUndo(); p = 1;
        { Group group(d); d.DeleteChars(1, 2); } State("explicit-replacement-after-rollback", d);
        ScalarAuto(d, p, "X", true); ScalarAuto(d, p, "Y", true);
        State("explicit-replacement-overwrite-XY", d);
        Undo(d, "explicit-replacement-undo1"); Undo(d, "explicit-replacement-undo2");
        Undo(d, "explicit-replacement-undo3");
    }
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 1;
        d.TentativeStart(); ScalarAuto(d, p, "x", true); State("setovertype-before-cancel", d);
        d.TentativeUndo(); d.TentativeCommit(); State("setovertype-after-cancel", d);
        Undo(d, "setovertype-cancel-undo");
    }
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 1;
        d.TentativeStart(); ScalarAuto(d, p, "x", true); State("setovertype-overwrite-preedit-x", d);
        d.TentativeUndo(); p = 1; d.TentativeStart();
        ScalarAuto(d, p, "y", false); ScalarAuto(d, p, "z", false);
        State("setovertype-off-update-yz", d);
        d.TentativeUndo(); d.TentativeCommit(); State("setovertype-off-update-cancel", d);
    }
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 1;
        d.TentativeStart(); ScalarAuto(d, p, "x", false); State("setovertype-insert-preedit-x", d);
        d.TentativeUndo(); p = 1; d.TentativeStart();
        ScalarAuto(d, p, "y", true); ScalarAuto(d, p, "z", true);
        State("setovertype-on-update-yz", d);
        d.TentativeUndo(); d.TentativeCommit(); State("setovertype-on-update-cancel", d);
    }
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 1;
        d.TentativeStart(); ScalarAuto(d, p, "x", true);
        d.TentativeCommit(); State("setovertype-commit-before", d); Undo(d, "setovertype-commit-undo");
    }
    {
        Document d(DocumentOption::Default); Init(d, "abc"); Sci::Position p = 3;
        PlainInsert(d, p, "d"); State("prior-unit-after-d", d);
        d.TentativeStart(); Scalar(d, p, "x", true); State("cancel-prior-preedit-x", d);
        d.TentativeUndo(); State("cancel-prior-after", d); Undo(d, "cancel-prior-undo1");
    }
    return 0;
}
