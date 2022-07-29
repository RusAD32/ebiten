// Copyright 2019 The Ebiten Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package buffered

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2/internal/affine"
	"github.com/hajimehoshi/ebiten/v2/internal/atlas"
	"github.com/hajimehoshi/ebiten/v2/internal/graphics"
	"github.com/hajimehoshi/ebiten/v2/internal/graphicsdriver"
	"github.com/hajimehoshi/ebiten/v2/internal/shaderir"
)

type Image struct {
	img    *atlas.Image
	width  int
	height int

	pixels []byte
}

func (i *Image) DebugInfo() atlas.ImageDebugInfo {
	return i.img.DebugInfo()
}

func BeginFrame(graphicsDriver graphicsdriver.Graphics) error {
	if err := atlas.BeginFrame(graphicsDriver); err != nil {
		return err
	}
	flushDelayedCommands()
	return nil
}

func EndFrame(graphicsDriver graphicsdriver.Graphics) error {
	return atlas.EndFrame(graphicsDriver)
}

func NewImage(width, height int, imageType atlas.ImageType) *Image {
	i := &Image{
		width:  width,
		height: height,
	}
	i.initialize(imageType)
	return i
}

func (i *Image) initialize(imageType atlas.ImageType) {
	if maybeCanAddDelayedCommand() {
		if tryAddDelayedCommand(func() {
			i.initialize(imageType)
		}) {
			return
		}
	}
	i.img = atlas.NewImage(i.width, i.height, imageType)
}

func (i *Image) invalidatePixels() {
	i.pixels = nil
}

func (i *Image) MarkDisposed() {
	if maybeCanAddDelayedCommand() {
		if tryAddDelayedCommand(func() {
			i.MarkDisposed()
		}) {
			return
		}
	}
	i.invalidatePixels()
	i.img.MarkDisposed()
}

func (img *Image) At(graphicsDriver graphicsdriver.Graphics, x, y int) (r, g, b, a byte, err error) {
	checkDelayedCommandsFlushed("At")

	idx := (y*img.width + x)
	if img.pixels != nil {
		return img.pixels[4*idx], img.pixels[4*idx+1], img.pixels[4*idx+2], img.pixels[4*idx+3], nil
	}

	pix, err := img.img.Pixels(graphicsDriver)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	img.pixels = pix
	return img.pixels[4*idx], img.pixels[4*idx+1], img.pixels[4*idx+2], img.pixels[4*idx+3], nil
}

func (i *Image) DumpScreenshot(graphicsDriver graphicsdriver.Graphics, name string, blackbg bool) error {
	checkDelayedCommandsFlushed("Dump")
	return i.img.DumpScreenshot(graphicsDriver, name, blackbg)
}

// ReplacePixels replaces the pixels at the specified region.
func (i *Image) ReplacePixels(pix []byte, x, y, width, height int) {
	if l := 4 * width * height; len(pix) != l {
		panic(fmt.Sprintf("buffered: len(pix) was %d but must be %d", len(pix), l))
	}

	if maybeCanAddDelayedCommand() {
		copied := make([]byte, len(pix))
		copy(copied, pix)
		if tryAddDelayedCommand(func() {
			i.ReplacePixels(copied, x, y, width, height)
		}) {
			return
		}
	}

	i.invalidatePixels()
	i.img.ReplacePixels(pix, x, y, width, height)
}

// DrawTriangles draws the src image with the given vertices.
//
// Copying vertices and indices is the caller's responsibility.
func (i *Image) DrawTriangles(srcs [graphics.ShaderImageCount]*Image, vertices []float32, indices []uint16, colorm affine.ColorM, mode graphicsdriver.CompositeMode, filter graphicsdriver.Filter, address graphicsdriver.Address, dstRegion, srcRegion graphicsdriver.Region, subimageOffsets [graphics.ShaderImageCount - 1][2]float32, shader *Shader, uniforms [][]float32, evenOdd bool) {
	for _, src := range srcs {
		if i == src {
			panic("buffered: Image.DrawTriangles: source images must be different from the receiver")
		}
	}

	if maybeCanAddDelayedCommand() {
		if tryAddDelayedCommand(func() {
			// Arguments are not copied. Copying is the caller's responsibility.
			i.DrawTriangles(srcs, vertices, indices, colorm, mode, filter, address, dstRegion, srcRegion, subimageOffsets, shader, uniforms, evenOdd)
		}) {
			return
		}
	}

	var s *atlas.Shader
	var imgs [graphics.ShaderImageCount]*atlas.Image
	if shader == nil {
		// Fast path for rendering without a shader (#1355).
		img := srcs[0]
		imgs[0] = img.img
	} else {
		for i, img := range srcs {
			if img == nil {
				continue
			}
			imgs[i] = img.img
		}
		s = shader.shader
	}

	i.invalidatePixels()
	i.img.DrawTriangles(imgs, vertices, indices, colorm, mode, filter, address, dstRegion, srcRegion, subimageOffsets, s, uniforms, evenOdd)
}

type Shader struct {
	shader *atlas.Shader
}

func NewShader(ir *shaderir.Program) *Shader {
	s := &Shader{}
	s.initialize(ir)
	return s
}

func (s *Shader) initialize(ir *shaderir.Program) {
	if maybeCanAddDelayedCommand() {
		if tryAddDelayedCommand(func() {
			s.initialize(ir)
		}) {
			return
		}
	}
	s.shader = atlas.NewShader(ir)
}

func (s *Shader) MarkDisposed() {
	if maybeCanAddDelayedCommand() {
		if tryAddDelayedCommand(func() {
			s.MarkDisposed()
		}) {
			return
		}
	}
	s.shader.MarkDisposed()
	s.shader = nil
}
