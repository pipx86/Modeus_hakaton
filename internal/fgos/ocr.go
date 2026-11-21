package fgos

import "context"

// OCRExtractor — внешний распознаватель сканов. Реализуется ocr.Document;
// nil означает прежнее поведение без OCR (скан возвращает ErrNoTextLayer).
//
// PagesFromPDF получает путь к PDF и возвращает страницы всего документа:
// страницы с нормальным текстовым слоем и распознанные страницы. Вызывающий
// код передаёт путь к временному файлу, поэтому реализация не должна сохранять
// его после возврата.
//
// RecognizeImage получает путь к PNG/JPEG и возвращает одну страницу с
// распознанным текстом.
type OCRExtractor interface {
	PagesFromPDF(ctx context.Context, path string) ([]Page, error)
	RecognizeImage(ctx context.Context, imagePath string) ([]Page, error)
}

// ContextDocumentExtractor — DocumentExtractor, который умеет принимать
// контекст: HTTP-обработчик импорта передаёт в него общий таймаут запроса,
// чтобы OCR и LLM не работали дольше, чем ждёт клиент. Проверяется через
// type assertion; старые реализации DocumentExtractor продолжают работать.
type ContextDocumentExtractor interface {
	DocumentExtractor
	ExtractPagesContext(ctx context.Context, name string, data []byte) ([]Page, error)
}
