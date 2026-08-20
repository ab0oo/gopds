// SPDX-License-Identifier: MIT

package normalize

import (
	"image"
	"io"

	_ "image/jpeg" // register JPEG decoder for cover dimension probing
	_ "image/png"  // register PNG decoder for cover dimension probing
)

func decodeConfig(r io.Reader) (image.Config, string, error) {
	return image.DecodeConfig(r)
}
