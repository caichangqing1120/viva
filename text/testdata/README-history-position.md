# Original history-position fixture

`scintilla-history-position.tsv` is copied byte-identically from the accepted
VivaPad 131 reference fixture:

`internal/changehistory/testdata/scintilla-history-position.tsv`

It was generated from unchanged pinned Notepad++ v8.9.8.1 commit
`dd40fe45598791e9aa38f3aeb200e10fb3d6338a`; see
`docs/specs/001-vivapad/history-position-reference.md` in the application work
package. SHA-256:
`86a18c4681356aa6b61c03d2fab97c6416ba2ba8f1981a75dfb48e06d59218ed`.

The file is read-only expected data. Tests parse it as-is; they never rewrite
or synthesize expected rows.
