// Package images holds the picture `toktape --image builtin` sends (TTP-208).
//
// testimage.png is made by tools/gen-test-image and committed; a test in that
// tool regenerates it and compares the bytes, so the file and its generator
// cannot disagree. The shapes and their places are listed in the generator's
// header, which is what makes the model's description checkable.
package images

import _ "embed"

// BuiltinName is the name a built-in image carries in a tape, and the
// argument that selects it.
const BuiltinName = "builtin"

// Question is what an --image run asks when no --prompt was given.
const Question = "Describe this image in detail: every object, its colour and where it is."

//go:embed testimage.png
var testImage []byte

// Builtin returns a copy of the built-in test image's PNG bytes.
func Builtin() []byte { return append([]byte(nil), testImage...) }
