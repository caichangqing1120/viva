# Original tentative-composition source reference

`composition-source-reference.txt` is the byte-identical output of the
self-contained probe source in this directory:

- `composition-source-probe.cxx` SHA-256
  `29fcd430fb58195eec1b1e98b44368fef1342669d67226e4f4e8c301568ca0ef`
- `composition-source-reference.txt` SHA-256
  `2a8fb641d2ec0a89a71261acc13b87c82fed71ffa7a8ae44ce00a28b69be3883`

The probe compiles unchanged pinned Notepad++/Scintilla v8.9.8.1 sources at commit
`dd40fe45598791e9aa38f3aeb200e10fb3d6338a`:

- `src/Document.cxx` `b67676dbe5f7ba357d3258582b127302d5e4b6b35de959c4e4124f2e2fec857a`
- `src/CellBuffer.cxx` `d51afe836fdd95c77d6dc2b379ef28def01bcbe2927e86d4516eb0fcf5ad3ec4`
- `src/UndoHistory.cxx` `bc7a3e1792c3d89ac54643af0a610113cf590cfeee42b5d4a898b9f148ef62a7`
- `src/Editor.cxx` `305561a566b373a6aa4b1431d3f87c63b82cd6be0513d56fbbc13c38e4fdef7b`

Reproducible build recipe (portable; set `SCINTILLA` to the pinned source root
and `OUT` to a writable build directory):

```sh
SDK=$(xcrun --show-sdk-path)
CXX=${CXX:-clang++}
FLAGS="-std=c++20 -O2 -DNDEBUG -fno-sanitize=all -isysroot $SDK -nostdinc++ \
  -I $SDK/usr/include/c++/v1 -I $SCINTILLA/include -I $SCINTILLA/src"
for f in Document CharClassify CharacterCategoryMap Decoration RESearch \
         CellBuffer UndoHistory PerLine RunStyles UniConversion ChangeHistory; do
  "$CXX" $FLAGS -c "$SCINTILLA/src/$f.cxx" -o "$OUT/$f.o"
done
"$CXX" $FLAGS text/testdata/composition-source-probe.cxx \
  "$OUT"/*.o -o "$OUT/probe_review"
"$OUT/probe_review" > "$OUT/probe_review.out"
shasum -a 256 "$OUT/probe_review.out"
```

The expected final SHA-256 is the reference hash above. The probe was built with
clang++ 22.1.5; any conforming C++20 compiler is acceptable if the pinned source
hashes match and the output is byte-identical.

The probe calls the actual source `InsertString`, `DeleteChars`,
`TentativeStart`, `TentativeUndo`, `TentativeCommit`, `SetSavePoint`,
`DeleteUndoHistory`, `Undo`, and `GetRelativePositionUTF16` boundaries. The
file is read-only expected data: tests parse it as-is and never rewrite or
synthesize its decisions in Go.

Scope: this evidence covers the source flows listed in the E1 report, including
the two accepted tentative callback streams. The current coordinator
`composition-original-callback-probe` compares those callback rows directly via
the combined read-only overlay; endpoint-only comparisons do not prove every
intermediate callback/context. This fixture does not claim coverage of every
parser, engine, platform or physical/native callback family.

# Exact-Git overwrite reference (E2)

`composition-overwrite-reference.txt` is the byte-identical 80-line output of
the self-contained Document/caller-boundary probe
`composition-overwrite-probe.cxx`:

- probe source SHA-256
  `3fcabe7723718edd6c00ecb87aadab235c57db7cfe8d3518b8b202480cbfb265`
- reference output SHA-256
  `d68a93d105fcb416c6512ce90f1248d98f81d0fb5bd1e2bd76267356c547d72b`
  (5,122 bytes)

Both were verified against the exact Git tree
`dd40fe45598791e9aa38f3aeb200e10fb3d6338a`, never the rejected zip. The
compiled set is the 16 units in the audit compile manifest; the exact source
hashes used here are:

- `scintilla/src/Editor.cxx`
  `305561a566b373a6aa4b1431d3f87c63b82cd6be0513d56fbbc13c38e4fdef7b`
  (`InsertCharacter` `:2087-2126`, `ClearBeforeTentativeStart` `:2192-2202`,
  `Message::SetOvertype` `:8564-8572`)
- `scintilla/src/Document.cxx`
  `b67676dbe5f7ba357d3258582b127302d5e4b6b35de959c4e4124f2e2fec857a`
  (`LenChar`/line-end protection `:560-566`, `:756-787`)
- `scintilla/src/UndoHistory.cxx`
  `bc7a3e1792c3d89ac54643af0a610113cf590cfeee42b5d4a898b9f148ef62a7`
  (`:335-352`, `:565-579`)
- `scintilla/win32/ScintillaWin.cxx`
  `3dffad6d56e100ff2275ce76fc5436d7b38ab1a1c696d6615946a3f9f2171246`
- `scintilla/cocoa/ScintillaCocoa.mm`
  `773e7deaf285a5965b331226b089a9a8abb53877d266e5dc0f1482ec4ca35b51`
- plus the remaining compiled units recorded in the owned E2 audit scratch
  `cpp-git-probe/compile-manifest.json` (all 16 independently verified).

The probe does not execute an Editor harness. It compiles exact Git `Document`,
`CellBuffer`, `UndoHistory`, `ChangeHistory` and supports, then replicates only
`Editor.cxx:2111-2122` selection/empty-selection overwrite and one `UndoGroup`
per call. Reproducible recipe (portable; set `SCINTILLA` to the pinned source
root and `OUT` to a writable build directory):

```sh
SDK=$(xcrun --sdk macosx --show-sdk-path)
S=$SCINTILLA/src
/usr/bin/clang++ -std=c++17 -stdlib=libc++ -isysroot "$SDK" \
  -isystem "$SDK/usr/include/c++/v1" -O0 \
  -I "$SCINTILLA/include" -I "$S" \
  text/testdata/composition-overwrite-probe.cxx "$S/Document.cxx" \
  "$S/CellBuffer.cxx" "$S/UndoHistory.cxx" "$S/ChangeHistory.cxx" \
  "$S/CharClassify.cxx" "$S/CaseFolder.cxx" "$S/CaseConvert.cxx" \
  "$S/CharacterCategoryMap.cxx" "$S/CharacterType.cxx" "$S/DBCS.cxx" \
  "$S/Decoration.cxx" "$S/PerLine.cxx" "$S/RunStyles.cxx" \
  "$S/UniConversion.cxx" "$S/UniqueString.cxx" "$S/RESearch.cxx" \
  -o "$OUT/probe_document_overwrite"
"$OUT/probe_document_overwrite" > "$OUT/probe_document_overwrite.out"
shasum -a 256 "$OUT/probe_document_overwrite.out"
```

The expected output hash is the reference hash above; it was built with native
clang++ in this environment. The fixture is read-only expected data. It proves
source byte/unit/action facts and initial Undo state, not a live native UI
composition callback stream; broader parser/engine/native families remain
separate.
