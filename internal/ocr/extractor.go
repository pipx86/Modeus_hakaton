package ocr

import (
	"context"

	"activity-descriptor/internal/fgos"
)

// FGOSExtractor — LLM-адаптер fgos.Extractor, который перед извлечением
// компетенций распознаёт страницы без текстового слоя. Встраивание сохраняет
// метод Extract (LLM-часть), а ExtractPages заменяется на OCR-версию.
//
// Используется в cmd/server и cmd/program-watcher:
//
//	extractor := ocr.NewFGOSExtractor(fgos.NewExtractor(client), ocr.NewDocument(cfg))
type FGOSExtractor struct {
	*fgos.Extractor
	// Document — OCR-пайплайн. Не должен быть nil; nil отключает OCR.
	Document *Document
}

// NewFGOSExtractor связывает LLM-адаптер с OCR.
func NewFGOSExtractor(extractor *fgos.Extractor, document *Document) *FGOSExtractor {
	return &FGOSExtractor{Extractor: extractor, Document: document}
}

// ExtractPages возвращает страницы документа, распознавая сканы при
// необходимости. Контекст не передаётся: у Document есть собственный таймаут
// OCR_TIMEOUT_S.
func (e *FGOSExtractor) ExtractPages(name string, data []byte) ([]fgos.Page, error) {
	return fgos.ExtractPagesWithOCR(context.Background(), name, data, e.ocr())
}

// ExtractPagesContext — вариант с контекстом HTTP-запроса: общий таймаут
// импорта ограничивает и OCR, и последующее извлечение компетенций.
func (e *FGOSExtractor) ExtractPagesContext(ctx context.Context, name string, data []byte) ([]fgos.Page, error) {
	return fgos.ExtractPagesWithOCR(ctx, name, data, e.ocr())
}

func (e *FGOSExtractor) ocr() fgos.OCRExtractor {
	if e == nil || e.Document == nil {
		return nil
	}
	return e.Document
}

var _ fgos.DocumentExtractor = (*FGOSExtractor)(nil)
var _ fgos.ContextDocumentExtractor = (*FGOSExtractor)(nil)
