// Copyright 2016 - 2023 The excelize Authors. All rights reserved. Use of
// this source code is governed by a BSD-style license that can be found in
// the LICENSE file.
//
// Package excelize providing a set of functions that allow you to write to and
// read from XLAM / XLSM / XLSX / XLTM / XLTX files. Supports reading and
// writing spreadsheet documents generated by Microsoft Excel™ 2007 and later.
// Supports complex components by high compatibility, and provided streaming
// API for generating or reading data from a worksheet with huge amounts of
// data. This library needs Go version 1.16 or later.

package excelize

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// SlicerOptions represents the settings of the slicer.
//
// Name specifies the slicer name, should be an existing field name of the given
// table or pivot table, this setting is required.
//
// Table specifies the name of the table or pivot table, this setting is
// required.
//
// Cell specifies the left top cell coordinates the position for inserting the
// slicer, this setting is required.
//
// Caption specifies the caption of the slicer, this setting is optional.
//
// Macro used for set macro for the slicer, the workbook extension should be
// XLSM or XLTM.
//
// Width specifies the width of the slicer, this setting is optional.
//
// Height specifies the height of the slicer, this setting is optional.
//
// DisplayHeader specifies if display header of the slicer, this setting is
// optional, the default setting is display.
//
// ItemDesc specifies descending (Z-A) item sorting, this setting is optional,
// and the default setting is false (represents ascending).
//
// Format specifies the format of the slicer, this setting is optional.
type SlicerOptions struct {
	Name          string
	Table         string
	Cell          string
	Caption       string
	Macro         string
	Width         uint
	Height        uint
	DisplayHeader *bool
	ItemDesc      bool
	Format        GraphicOptions
}

// AddSlicer function inserts a slicer by giving the worksheet name and slicer
// settings. The pivot table slicer is not supported currently.
//
// For example, insert a slicer on the Sheet1!E1 with field Column1 for the
// table named Table1:
//
//	err := f.AddSlicer("Sheet1", &excelize.SlicerOptions{
//	    Name:    "Column1",
//	    Table:   "Table1",
//	    Cell:    "E1",
//	    Caption: "Column1",
//	    Width:   200,
//	    Height:  200,
//	})
func (f *File) AddSlicer(sheet string, opts *SlicerOptions) error {
	opts, err := parseSlicerOptions(opts)
	if err != nil {
		return err
	}
	table, colIdx, err := f.getSlicerSource(sheet, opts)
	if err != nil {
		return err
	}
	slicerID, err := f.addSheetSlicer(sheet)
	if err != nil {
		return err
	}
	slicerCacheName, err := f.setSlicerCache(colIdx, opts, table)
	if err != nil {
		return err
	}
	slicerName, err := f.addDrawingSlicer(sheet, opts)
	if err != nil {
		return err
	}
	return f.addSlicer(slicerID, xlsxSlicer{
		Name:        slicerName,
		Cache:       slicerCacheName,
		Caption:     opts.Caption,
		ShowCaption: opts.DisplayHeader,
		RowHeight:   251883,
	})
}

// parseSlicerOptions provides a function to parse the format settings of the
// slicer with default value.
func parseSlicerOptions(opts *SlicerOptions) (*SlicerOptions, error) {
	if opts == nil {
		return nil, ErrParameterRequired
	}
	if opts.Name == "" || opts.Table == "" || opts.Cell == "" {
		return nil, ErrParameterInvalid
	}
	if opts.Width == 0 {
		opts.Width = defaultSlicerWidth
	}
	if opts.Height == 0 {
		opts.Height = defaultSlicerHeight
	}
	if opts.Format.PrintObject == nil {
		opts.Format.PrintObject = boolPtr(true)
	}
	if opts.Format.Locked == nil {
		opts.Format.Locked = boolPtr(false)
	}
	if opts.Format.ScaleX == 0 {
		opts.Format.ScaleX = defaultDrawingScale
	}
	if opts.Format.ScaleY == 0 {
		opts.Format.ScaleY = defaultDrawingScale
	}
	return opts, nil
}

// countSlicers provides a function to get slicer files count storage in the
// folder xl/slicers.
func (f *File) countSlicers() int {
	count := 0
	f.Pkg.Range(func(k, v interface{}) bool {
		if strings.Contains(k.(string), "xl/slicers/slicer") {
			count++
		}
		return true
	})
	return count
}

// countSlicerCache provides a function to get slicer cache files count storage
// in the folder xl/SlicerCaches.
func (f *File) countSlicerCache() int {
	count := 0
	f.Pkg.Range(func(k, v interface{}) bool {
		if strings.Contains(k.(string), "xl/slicerCaches/slicerCache") {
			count++
		}
		return true
	})
	return count
}

// getSlicerSource returns the slicer data source table or pivot table settings
// and the index of the given slicer fields in the table or pivot table
// column.
func (f *File) getSlicerSource(sheet string, opts *SlicerOptions) (*Table, int, error) {
	var (
		table       *Table
		colIdx      int
		tables, err = f.GetTables(sheet)
	)
	if err != nil {
		return table, colIdx, err
	}
	for _, tbl := range tables {
		if tbl.Name == opts.Table {
			table = &tbl
			break
		}
	}
	if table == nil {
		return table, colIdx, newNoExistTableError(opts.Table)
	}
	order, _ := f.getTableFieldsOrder(sheet, fmt.Sprintf("%s!%s", sheet, table.Range))
	if colIdx = inStrSlice(order, opts.Name, true); colIdx == -1 {
		return table, colIdx, newInvalidSlicerNameError(opts.Name)
	}
	return table, colIdx, err
}

// addSheetSlicer adds a new slicer and updates the namespace and relationships
// parts of the worksheet by giving the worksheet name.
func (f *File) addSheetSlicer(sheet string) (int, error) {
	var (
		slicerID     = f.countSlicers() + 1
		ws, err      = f.workSheetReader(sheet)
		decodeExtLst = new(decodeExtLst)
		slicerList   = new(decodeSlicerList)
	)
	if err != nil {
		return slicerID, err
	}
	if ws.ExtLst != nil {
		if err = f.xmlNewDecoder(strings.NewReader("<extLst>" + ws.ExtLst.Ext + "</extLst>")).
			Decode(decodeExtLst); err != nil && err != io.EOF {
			return slicerID, err
		}
		for _, ext := range decodeExtLst.Ext {
			if ext.URI == ExtURISlicerListX15 {
				_ = f.xmlNewDecoder(strings.NewReader(ext.Content)).Decode(slicerList)
				for _, slicer := range slicerList.Slicer {
					if slicer.RID != "" {
						sheetRelationshipsDrawingXML := f.getSheetRelationshipsTargetByID(sheet, slicer.RID)
						slicerID, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(sheetRelationshipsDrawingXML, "../slicers/slicer"), ".xml"))
						return slicerID, err
					}
				}
			}
		}
	}
	sheetRelationshipsSlicerXML := "../slicers/slicer" + strconv.Itoa(slicerID) + ".xml"
	sheetXMLPath, _ := f.getSheetXMLPath(sheet)
	sheetRels := "xl/worksheets/_rels/" + strings.TrimPrefix(sheetXMLPath, "xl/worksheets/") + ".rels"
	rID := f.addRels(sheetRels, SourceRelationshipSlicer, sheetRelationshipsSlicerXML, "")
	f.addSheetNameSpace(sheet, NameSpaceSpreadSheetX14)
	return slicerID, f.addSheetTableSlicer(ws, rID)
}

// addSheetTableSlicer adds a new table slicer for the worksheet by giving the
// worksheet relationships ID.
func (f *File) addSheetTableSlicer(ws *xlsxWorksheet, rID int) error {
	var (
		decodeExtLst                 = new(decodeExtLst)
		err                          error
		slicerListBytes, extLstBytes []byte
	)
	if ws.ExtLst != nil {
		if err = f.xmlNewDecoder(strings.NewReader("<extLst>" + ws.ExtLst.Ext + "</extLst>")).
			Decode(decodeExtLst); err != nil && err != io.EOF {
			return err
		}
	}
	slicerListBytes, _ = xml.Marshal(&xlsxX14SlicerList{
		Slicer: []*xlsxX14Slicer{{RID: "rId" + strconv.Itoa(rID)}},
	})
	decodeExtLst.Ext = append(decodeExtLst.Ext, &xlsxExt{
		xmlns: []xml.Attr{{Name: xml.Name{Local: "xmlns:" + NameSpaceSpreadSheetX15.Name.Local}, Value: NameSpaceSpreadSheetX15.Value}},
		URI:   ExtURISlicerListX15, Content: string(slicerListBytes),
	})
	sort.Slice(decodeExtLst.Ext, func(i, j int) bool {
		return inStrSlice(extensionURIPriority, decodeExtLst.Ext[i].URI, false) <
			inStrSlice(extensionURIPriority, decodeExtLst.Ext[j].URI, false)
	})
	extLstBytes, err = xml.Marshal(decodeExtLst)
	ws.ExtLst = &xlsxExtLst{Ext: strings.TrimSuffix(strings.TrimPrefix(string(extLstBytes), "<extLst>"), "</extLst>")}
	return err
}

// addSlicer adds a new slicer to the workbook by giving the slicer ID and
// settings.
func (f *File) addSlicer(slicerID int, slicer xlsxSlicer) error {
	slicerXML := "xl/slicers/slicer" + strconv.Itoa(slicerID) + ".xml"
	slicers, err := f.slicerReader(slicerXML)
	if err != nil {
		return err
	}
	if err := f.addContentTypePart(slicerID, "slicer"); err != nil {
		return err
	}
	slicers.Slicer = append(slicers.Slicer, slicer)
	output, err := xml.Marshal(slicers)
	f.saveFileList(slicerXML, output)
	return err
}

// genSlicerNames generates a unique slicer cache name by giving the slicer name.
func (f *File) genSlicerCacheName(name string) string {
	var (
		cnt             int
		definedNames    []string
		slicerCacheName string
	)
	for _, dn := range f.GetDefinedName() {
		if dn.Scope == "Workbook" {
			definedNames = append(definedNames, dn.Name)
		}
	}
	for i, c := range name {
		if unicode.IsLetter(c) {
			slicerCacheName += string(c)
			continue
		}
		if i > 0 && (unicode.IsDigit(c) || c == '.') {
			slicerCacheName += string(c)
			continue
		}
		slicerCacheName += "_"
	}
	slicerCacheName = fmt.Sprintf("Slicer_%s", slicerCacheName)
	for {
		tmp := slicerCacheName
		if cnt > 0 {
			tmp = fmt.Sprintf("%s%d", slicerCacheName, cnt)
		}
		if inStrSlice(definedNames, tmp, true) == -1 {
			slicerCacheName = tmp
			break
		}
		cnt++
	}
	return slicerCacheName
}

// setSlicerCache check if a slicer cache already exists or add a new slicer
// cache by giving the column index, slicer, table options, and returns the
// slicer cache name.
func (f *File) setSlicerCache(colIdx int, opts *SlicerOptions, table *Table) (string, error) {
	var ok bool
	var slicerCacheName string
	f.Pkg.Range(func(k, v interface{}) bool {
		if strings.Contains(k.(string), "xl/slicerCaches/slicerCache") {
			slicerCache := &xlsxSlicerCacheDefinition{}
			if err := f.xmlNewDecoder(bytes.NewReader(namespaceStrictToTransitional(v.([]byte)))).
				Decode(slicerCache); err != nil && err != io.EOF {
				return true
			}
			if slicerCache.ExtLst == nil {
				return true
			}
			ext := new(xlsxExt)
			_ = f.xmlNewDecoder(strings.NewReader(slicerCache.ExtLst.Ext)).Decode(ext)
			if ext.URI == ExtURISlicerCacheDefinition {
				tableSlicerCache := new(decodeTableSlicerCache)
				_ = f.xmlNewDecoder(strings.NewReader(ext.Content)).Decode(tableSlicerCache)
				if tableSlicerCache.TableID == table.tID && tableSlicerCache.Column == colIdx+1 {
					ok, slicerCacheName = true, slicerCache.Name
					return false
				}
			}
		}
		return true
	})
	if ok {
		return slicerCacheName, nil
	}
	slicerCacheName = f.genSlicerCacheName(opts.Name)
	return slicerCacheName, f.addSlicerCache(slicerCacheName, colIdx, opts, table)
}

// slicerReader provides a function to get the pointer to the structure
// after deserialization of xl/slicers/slicer%d.xml.
func (f *File) slicerReader(slicerXML string) (*xlsxSlicers, error) {
	content, ok := f.Pkg.Load(slicerXML)
	slicer := &xlsxSlicers{
		XMLNSXMC:  SourceRelationshipCompatibility.Value,
		XMLNSX:    NameSpaceSpreadSheet.Value,
		XMLNSXR10: NameSpaceSpreadSheetXR10.Value,
	}
	if ok && content != nil {
		if err := f.xmlNewDecoder(bytes.NewReader(namespaceStrictToTransitional(content.([]byte)))).
			Decode(slicer); err != nil && err != io.EOF {
			return nil, err
		}
	}
	return slicer, nil
}

// addSlicerCache adds a new slicer cache by giving the slicer cache name,
// column index, slicer, and table options.
func (f *File) addSlicerCache(slicerCacheName string, colIdx int, opts *SlicerOptions, table *Table) error {
	var (
		slicerCacheBytes, tableSlicerBytes, extLstBytes []byte
		slicerCacheID                                   = f.countSlicerCache() + 1
		decodeExtLst                                    = new(decodeExtLst)
		slicerCache                                     = xlsxSlicerCacheDefinition{
			XMLNSXMC:   SourceRelationshipCompatibility.Value,
			XMLNSX:     NameSpaceSpreadSheet.Value,
			XMLNSX15:   NameSpaceSpreadSheetX15.Value,
			XMLNSXR10:  NameSpaceSpreadSheetXR10.Value,
			Name:       slicerCacheName,
			SourceName: opts.Name,
			ExtLst:     &xlsxExtLst{},
		}
	)
	var sortOrder string
	if opts.ItemDesc {
		sortOrder = "descending"
	}
	tableSlicerBytes, _ = xml.Marshal(&xlsxTableSlicerCache{
		TableID:   table.tID,
		Column:    colIdx + 1,
		SortOrder: sortOrder,
	})
	decodeExtLst.Ext = append(decodeExtLst.Ext, &xlsxExt{
		xmlns: []xml.Attr{{Name: xml.Name{Local: "xmlns:" + NameSpaceSpreadSheetX15.Name.Local}, Value: NameSpaceSpreadSheetX15.Value}},
		URI:   ExtURISlicerCacheDefinition, Content: string(tableSlicerBytes),
	})
	extLstBytes, _ = xml.Marshal(decodeExtLst)
	slicerCache.ExtLst = &xlsxExtLst{Ext: strings.TrimSuffix(strings.TrimPrefix(string(extLstBytes), "<extLst>"), "</extLst>")}
	slicerCacheXML := "xl/slicerCaches/slicerCache" + strconv.Itoa(slicerCacheID) + ".xml"
	slicerCacheBytes, _ = xml.Marshal(slicerCache)
	f.saveFileList(slicerCacheXML, slicerCacheBytes)
	if err := f.addContentTypePart(slicerCacheID, "slicerCache"); err != nil {
		return err
	}
	if err := f.addWorkbookSlicerCache(slicerCacheID, ExtURISlicerCachesX15); err != nil {
		return err
	}
	return f.SetDefinedName(&DefinedName{Name: slicerCacheName, RefersTo: formulaErrorNA})
}

// addDrawingSlicer adds a slicer shape and fallback shape by giving the
// worksheet name, slicer options, and returns slicer name.
func (f *File) addDrawingSlicer(sheet string, opts *SlicerOptions) (string, error) {
	var slicerName string
	drawingID := f.countDrawings() + 1
	drawingXML := "xl/drawings/drawing" + strconv.Itoa(drawingID) + ".xml"
	ws, err := f.workSheetReader(sheet)
	if err != nil {
		return slicerName, err
	}
	drawingID, drawingXML = f.prepareDrawing(ws, drawingID, sheet, drawingXML)
	content, twoCellAnchor, cNvPrID, err := f.twoCellAnchorShape(sheet, drawingXML, opts.Cell, opts.Width, opts.Height, opts.Format)
	if err != nil {
		return slicerName, err
	}
	slicerName = fmt.Sprintf("%s %d", opts.Name, cNvPrID)
	graphicFrame := xlsxGraphicFrame{
		NvGraphicFramePr: xlsxNvGraphicFramePr{
			CNvPr: &xlsxCNvPr{
				ID:   cNvPrID,
				Name: slicerName,
			},
		},
		Xfrm: xlsxXfrm{Off: xlsxOff{}, Ext: aExt{}},
		Graphic: &xlsxGraphic{
			GraphicData: &xlsxGraphicData{
				URI: NameSpaceDrawingMLSlicer.Value,
				Sle: &xlsxSle{XMLNS: NameSpaceDrawingMLSlicer.Value, Name: slicerName},
			},
		},
	}
	graphic, _ := xml.Marshal(graphicFrame)
	sp := xdrSp{
		Macro: opts.Macro,
		NvSpPr: &xdrNvSpPr{
			CNvPr: &xlsxCNvPr{
				ID: cNvPrID,
			},
			CNvSpPr: &xdrCNvSpPr{
				TxBox: true,
			},
		},
		SpPr: &xlsxSpPr{
			Xfrm:      xlsxXfrm{Off: xlsxOff{X: 2914650, Y: 152400}, Ext: aExt{Cx: 1828800, Cy: 2238375}},
			SolidFill: &xlsxInnerXML{Content: "<a:prstClr val=\"white\"/>"},
			PrstGeom: xlsxPrstGeom{
				Prst: "rect",
			},
			Ln: xlsxLineProperties{W: 1, SolidFill: &xlsxInnerXML{Content: "<a:prstClr val=\"black\"/>"}},
		},
		TxBody: &xdrTxBody{
			BodyPr: &aBodyPr{VertOverflow: "clip", HorzOverflow: "clip"},
			P: []*aP{
				{R: &aR{T: "This shape represents a table slicer. Table slicers are not supported in this version of Excel."}},
				{R: &aR{T: "If the shape was modified in an earlier version of Excel, or if the workbook was saved in Excel 2007 or earlier, the slicer can't be used."}},
			},
		},
	}
	shape, _ := xml.Marshal(sp)
	twoCellAnchor.ClientData = &xdrClientData{
		FLocksWithSheet:  *opts.Format.Locked,
		FPrintsWithSheet: *opts.Format.PrintObject,
	}
	choice := xlsxChoice{
		XMLNSSle15: NameSpaceDrawingMLSlicerX15.Value,
		Requires:   NameSpaceDrawingMLSlicerX15.Name.Local,
		Content:    string(graphic),
	}
	fallback := xlsxFallback{
		Content: string(shape),
	}
	choiceBytes, _ := xml.Marshal(choice)
	shapeBytes, _ := xml.Marshal(fallback)
	twoCellAnchor.AlternateContent = append(twoCellAnchor.AlternateContent, &xlsxAlternateContent{
		XMLNSMC: SourceRelationshipCompatibility.Value,
		Content: string(choiceBytes) + string(shapeBytes),
	})
	content.TwoCellAnchor = append(content.TwoCellAnchor, twoCellAnchor)
	f.Drawings.Store(drawingXML, content)
	return slicerName, f.addContentTypePart(drawingID, "drawings")
}

// addWorkbookSlicerCache add the association ID of the slicer cache in
// workbook.xml.
func (f *File) addWorkbookSlicerCache(slicerCacheID int, URI string) error {
	var (
		wb                                               *xlsxWorkbook
		err                                              error
		idx                                              int
		appendMode                                       bool
		decodeExtLst                                     = new(decodeExtLst)
		decodeSlicerCaches                               *decodeX15SlicerCaches
		x15SlicerCaches                                  = new(xlsxX15SlicerCaches)
		ext                                              *xlsxExt
		slicerCacheBytes, slicerCachesBytes, extLstBytes []byte
	)
	if wb, err = f.workbookReader(); err != nil {
		return err
	}
	rID := f.addRels(f.getWorkbookRelsPath(), SourceRelationshipSlicerCache, fmt.Sprintf("/xl/slicerCaches/slicerCache%d.xml", slicerCacheID), "")
	if wb.ExtLst != nil { // append mode ext
		if err = f.xmlNewDecoder(strings.NewReader("<extLst>" + wb.ExtLst.Ext + "</extLst>")).
			Decode(decodeExtLst); err != nil && err != io.EOF {
			return err
		}
		for idx, ext = range decodeExtLst.Ext {
			if ext.URI == URI {
				if URI == ExtURISlicerCachesX15 {
					decodeSlicerCaches = new(decodeX15SlicerCaches)
					_ = f.xmlNewDecoder(strings.NewReader(ext.Content)).Decode(decodeSlicerCaches)
					slicerCache := xlsxX14SlicerCache{RID: fmt.Sprintf("rId%d", rID)}
					slicerCacheBytes, _ = xml.Marshal(slicerCache)
					x15SlicerCaches.Content = decodeSlicerCaches.Content + string(slicerCacheBytes)
					x15SlicerCaches.XMLNS = NameSpaceSpreadSheetX14.Value
					slicerCachesBytes, _ = xml.Marshal(x15SlicerCaches)
					decodeExtLst.Ext[idx].Content = string(slicerCachesBytes)
					appendMode = true
				}
			}
		}
	}
	if !appendMode {
		if URI == ExtURISlicerCachesX15 {
			slicerCache := xlsxX14SlicerCache{RID: fmt.Sprintf("rId%d", rID)}
			slicerCacheBytes, _ = xml.Marshal(slicerCache)
			x15SlicerCaches.Content = string(slicerCacheBytes)
			x15SlicerCaches.XMLNS = NameSpaceSpreadSheetX14.Value
			slicerCachesBytes, _ = xml.Marshal(x15SlicerCaches)
			decodeExtLst.Ext = append(decodeExtLst.Ext, &xlsxExt{
				xmlns: []xml.Attr{{Name: xml.Name{Local: "xmlns:" + NameSpaceSpreadSheetX15.Name.Local}, Value: NameSpaceSpreadSheetX15.Value}},
				URI:   ExtURISlicerCachesX15, Content: string(slicerCachesBytes),
			})
		}
	}
	extLstBytes, err = xml.Marshal(decodeExtLst)
	wb.ExtLst = &xlsxExtLst{Ext: strings.TrimSuffix(strings.TrimPrefix(string(extLstBytes), "<extLst>"), "</extLst>")}
	return err
}