package programdocx

// Минимальный OOXML-пакет: этих частей достаточно Word и LibreOffice,
// чтобы открыть документ и показать текст, таблицу и параметры страницы.
const (
	xmlDeclaration = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

	contentTypesXML = xmlDeclaration +
		`<Types xmlns="` + namespaceContentTypes + `">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
		`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
		`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>` +
		`</Types>`

	rootRelationshipsXML = xmlDeclaration +
		`<Relationships xmlns="` + namespaceRelationships + `">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
		`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>` +
		`</Relationships>`

	documentRelationshipsXML = xmlDeclaration +
		`<Relationships xmlns="` + namespaceRelationships + `">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`</Relationships>`

	stylesXML = xmlDeclaration +
		`<w:styles xmlns:w="` + namespaceWord + `">` +
		`<w:docDefaults>` +
		`<w:rPrDefault><w:rPr>` +
		`<w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="Times New Roman" w:cs="Times New Roman"/>` +
		`<w:sz w:val="22"/><w:szCs w:val="22"/>` +
		`<w:lang w:val="ru-RU" w:eastAsia="ru-RU" w:bidi="ar-SA"/>` +
		`</w:rPr></w:rPrDefault>` +
		`<w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault>` +
		`</w:docDefaults>` +
		`<w:style w:type="paragraph" w:default="1" w:styleId="Normal">` +
		`<w:name w:val="Normal"/><w:qFormat/>` +
		`<w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="Times New Roman" w:cs="Times New Roman"/><w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr>` +
		`</w:style>` +
		`<w:style w:type="table" w:styleId="TableGrid">` +
		`<w:name w:val="Table Grid"/>` +
		`<w:tblPr><w:tblBorders>` +
		`<w:top w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`<w:left w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`<w:bottom w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`<w:right w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`<w:insideH w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`<w:insideV w:val="single" w:sz="4" w:space="0" w:color="000000"/>` +
		`</w:tblBorders></w:tblPr>` +
		`</w:style>` +
		`</w:styles>`

	appPropertiesXML = xmlDeclaration +
		`<Properties xmlns="` + namespaceExtendedProps + `" xmlns:vt="` + namespaceVT + `">` +
		`<Application>activity-descriptor</Application>` +
		`<DocSecurity>0</DocSecurity>` +
		`<ScaleCrop>false</ScaleCrop>` +
		`<LinksUpToDate>false</LinksUpToDate>` +
		`<SharedDoc>false</SharedDoc>` +
		`<HyperlinksChanged>false</HyperlinksChanged>` +
		`<AppVersion>16.0000</AppVersion>` +
		`</Properties>`
)

func corePropertiesXML(title, timestamp string) string {
	return xmlDeclaration +
		`<cp:coreProperties xmlns:cp="` + namespaceCoreProps + `" xmlns:dc="` + namespaceDC +
		`" xmlns:dcterms="` + namespaceDCTerms + `" xmlns:xsi="` + namespaceXSI + `">` +
		`<dc:title>` + escapeXML(cleanXMLText(title)) + `</dc:title>` +
		`<dc:creator>activity-descriptor</dc:creator>` +
		`<cp:lastModifiedBy>activity-descriptor</cp:lastModifiedBy>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + timestamp + `</dcterms:created>` +
		`<dcterms:modified xsi:type="dcterms:W3CDTF">` + timestamp + `</dcterms:modified>` +
		`</cp:coreProperties>`
}
