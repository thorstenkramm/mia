package material

import (
	"bytes"
	"io"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/fault"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// readMaterialPDF ignores only document metadata semantics. Check raw hazards before detaching references,
// and retain referenced objects because page content may share them. Source bytes are never rewritten.
func readMaterialPDF(source []byte, configuration *model.Configuration) (ctx *model.Context, err error) {
	defer fault.Catch(&err)
	if !bytes.HasPrefix(source, []byte("%PDF-")) || containsPDFHazard(source) {
		return nil, ErrInvalid
	}
	ctx, err = api.ReadContext(bytes.NewReader(source), configuration)
	if err != nil {
		return nil, err
	}
	if ctx.Encrypt != nil {
		return nil, ErrInvalid
	}
	ctx.Info = nil
	catalog, err := ctx.Catalog()
	if err != nil {
		return nil, err
	}
	delete(catalog, "Metadata")
	if err := api.ValidateContext(ctx); err != nil {
		return nil, err
	}
	if ctx.PageCount < 1 {
		return nil, ErrInvalid
	}
	return ctx, nil
}

// preparePDFChunks follows pdfcpu's TRIM preparation on the metadata-detached context.
func preparePDFChunks(source []byte) (ctx *model.Context, err error) {
	defer fault.Catch(&err)
	configuration := pdfConfiguration()
	configuration.Cmd = model.TRIM
	ctx, err = readMaterialPDF(source, configuration)
	if err != nil {
		return nil, err
	}
	if configuration.Optimize {
		if err := api.OptimizeContext(ctx); err != nil {
			return nil, err
		}
	}
	if err := pdfcpu.CacheFormFonts(ctx); err != nil {
		return nil, err
	}
	return ctx, nil
}

func writePDFChunk(ctx *model.Context, output io.Writer, first, last int) (err error) {
	defer fault.Catch(&err)
	pages := make([]int, 0, last-first+1)
	for page := first; page <= last; page++ {
		pages = append(pages, page)
	}
	destination, err := pdfcpu.ExtractPages(ctx, pages, false)
	if err != nil {
		return err
	}
	if ctx.PostProcessValidate {
		if err := api.ValidateContext(destination); err != nil {
			return err
		}
	}
	return api.WriteContext(destination, output)
}
