#include <cstdarg>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <optional>
#include <string>
#include <string_view>
#include <vector>
#include <cassert>
#include <forward_list>
#include <map>
#include <memory>

#include "ScintillaTypes.h"
#include "ILoader.h"
#include "ILexer.h"
#include "Debugging.h"
#include "CharacterType.h"
#include "CharacterCategoryMap.h"
#include "Position.h"
#include "SplitVector.h"
#include "Partitioning.h"
#include "RunStyles.h"
#include "SparseVector.h"
#include "ChangeHistory.h"
#include "CellBuffer.h"
#include "PerLine.h"
#include "CharClassify.h"
#include "Decoration.h"
#include "UndoHistory.h"
#include "CaseFolder.h"
#include "RESearch.h"
#include "UniConversion.h"
#include "Document.h"

using namespace Scintilla::Internal;

namespace Scintilla::Internal::Platform {
void Assert(const char *condition, const char *file, int line) noexcept {
    std::fprintf(stderr, "%s:%d: %s\n", file, line, condition); std::abort();
}
void DebugPrintf(const char *format, ...) noexcept {
    va_list args; va_start(args, format); std::vfprintf(stderr, format, args); va_end(args);
}
}

static std::string Text(Document &doc) {
    std::string text(static_cast<size_t>(doc.Length()), '\0');
    if (doc.Length() > 0) doc.GetCharRange(text.data(), 0, doc.Length());
    return text;
}
static void Insert(Document &doc, Sci::Position at, std::string_view value) { doc.InsertString(at, value); }
static void Erase(Document &doc, Sci::Position at, Sci::Position len) { doc.DeleteChars(at, len); }
static void UndoOne(Document &doc) { doc.Undo(); }
static void Sample(const char *name, Document &doc) {
    std::printf("%s text=%s current=%d actions=%d save=%d detach=%d canUndo=%d canRedo=%d\n", name, Text(doc).c_str(),
        doc.UndoCurrent(), doc.UndoActions(), doc.UndoSavePoint(), doc.UndoDetach(), doc.CanUndo()?1:0, doc.CanRedo()?1:0);
}
static void Base(Document &doc, std::string_view initial) {
    doc.SetUndoCollection(false); Insert(doc, 0, initial); doc.SetUndoCollection(true);
    doc.DeleteUndoHistory(); doc.SetSavePoint(); Sample("base", doc);
}
static Sci::Position Rel(Document &doc, Sci::Position start, Sci::Position units) {
    return doc.GetRelativePositionUTF16(start, units);
}
static void ShowRawRange(const char *name, Document &doc, int start, int length) {
    Sci::Position a = Rel(doc, 0, start);
    Sci::Position b = a == Sci::invalidPosition ? doc.Length() : Rel(doc, a, length);
    if (a == Sci::invalidPosition) a = doc.Length();
    if (b == Sci::invalidPosition) b = doc.Length();
    std::printf("%s utf16=%d..%d -> bytes=%ld..%ld\n", name, start, start+length, (long)a, (long)b);
}

int main() {
    {
        Document doc(Scintilla::DocumentOption::Default); Base(doc, "abc");
        Erase(doc, 1, 1); doc.TentativeStart(); Insert(doc, 1, "x"); Sample("ascii-pre", doc);
        doc.TentativeUndo(); Sample("ascii-rollback", doc);
        Erase(doc, 0, 1); Insert(doc, 0, "r"); Sample("ascii-replacement-result", doc);
        UndoOne(doc); Sample("ascii-replacement-undo1", doc);
        UndoOne(doc); Sample("ascii-replacement-undo2", doc);
        UndoOne(doc); Sample("ascii-replacement-undo3", doc);
    }
    {
        Document doc(Scintilla::DocumentOption::Default); Base(doc, "a\xc3\xa9" "\xf0\x9d\x84\x9e" "b");
        Erase(doc, 1, 6); doc.TentativeStart(); Insert(doc, 1, "x"); Sample("unicode-pre", doc);
        doc.TentativeUndo(); Sample("unicode-rollback", doc);
        Erase(doc, 0, 1); Insert(doc, 0, "r"); Sample("unicode-replacement-result", doc);
        UndoOne(doc); Sample("unicode-replacement-undo1", doc);
        UndoOne(doc); Sample("unicode-replacement-undo2", doc);
        UndoOne(doc); Sample("unicode-replacement-undo3", doc);
    }
    {
        // a + BMP e-acute + astral U+1D11E + c + d; target d is UTF16 5..6.
        Document doc(Scintilla::DocumentOption::Default); Base(doc, "abcd");
        Erase(doc, 1, 1); doc.TentativeStart();
        Insert(doc, 1, "\xc3\xa9");       // BMP scalar, 2 bytes
        Insert(doc, 3, "\xf0\x9d\x84\x9e"); // astral scalar, 4 bytes
        Sample("preedit-bmp-astral-visible", doc);
        ShowRawRange("preedit-bmp-astral-visible-range", doc, 5, 1);
        doc.TentativeUndo(); Sample("preedit-bmp-astral-rollback", doc);
        ShowRawRange("post-rollback-raw-range", doc, 5, 1);
        Erase(doc, 0, 0); // source PositionsFromCharacters yields empty 3..3: no delete
        Insert(doc, 3, "r"); Sample("preedit-bmp-astral-result", doc);
    }
    {
        Document doc(Scintilla::DocumentOption::Default); Base(doc, "abc");
        doc.TentativeStart(); Insert(doc, 3, "x"); Sample("no-selection-preedit", doc);
        doc.TentativeUndo(); Sample("no-selection-rollback", doc);
        Insert(doc, 3, "r"); Sample("no-selection-result", doc);
    }
    {
        Document doc(Scintilla::DocumentOption::Default); Base(doc, "abc");
        Insert(doc, 3, "d"); doc.SetSavePoint(); UndoOne(doc); Insert(doc, 3, "e");
        Sample("clear-detach-before", doc);
        doc.DeleteUndoHistory(); Sample("clear-detach-after", doc);
    }
    {
        Document doc(Scintilla::DocumentOption::Default); Base(doc, "abc");
        doc.TentativeStart(); Insert(doc, 3, "x"); Sample("clear-tentative-before", doc);
        doc.DeleteUndoHistory(); Sample("clear-tentative-after", doc);
        doc.TentativeUndo(); Sample("clear-tentative-undo-noop", doc);
        Insert(doc, 3, "y"); Sample("clear-tentative-next-insert", doc);
    }
    {
        Document doc(Scintilla::DocumentOption::Default); Base(doc, "abc");
        doc.TentativeStart(); Insert(doc, 3, "x"); doc.SetSavePoint(); Sample("clear-save-tentative-before", doc);
        doc.DeleteUndoHistory(); Sample("clear-save-tentative-after", doc);
        Insert(doc, 3, "y"); Sample("clear-save-tentative-next-insert", doc);
    }
    {
        Document doc(Scintilla::DocumentOption::Default); Base(doc, "abc");
        doc.TentativeStart(); Insert(doc, 3, "x"); doc.SetSavePoint(); Sample("save-in-tentative", doc);
        doc.TentativeCommit(); Sample("save-in-tentative-commit", doc);
    }
    {
        Document doc(Scintilla::DocumentOption::Default); Base(doc, "abc");
        doc.TentativeStart(); Insert(doc, 3, "x"); doc.SetSavePoint(); Sample("save-in-rollback-before", doc);
        doc.TentativeUndo(); Sample("save-in-rollback-after", doc);
        Insert(doc, 3, "y"); Sample("save-in-rollback-next-append", doc);
    }
    {
        Document doc(Scintilla::DocumentOption::Default); Base(doc, "abc");
        Erase(doc, 1, 1); Insert(doc, 1, "r"); Sample("mixed-after-result-r", doc);
        doc.TentativeStart(); Insert(doc, 2, "x"); Sample("mixed-after-preedit-x", doc);
        doc.TentativeUndo(); Sample("mixed-after-rollback-x", doc);
        Insert(doc, 2, "y"); Sample("mixed-after-result-y", doc);
    }
    return 0;
}
