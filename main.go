package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"mywalkapp/ftviewconverter"
)

// ---------------------------------------------------------------------
// Enums mirroring the C# OverwriteChoice / YesNoChoice enums in GUI.cs
// ---------------------------------------------------------------------

type OverwriteChoice int

const (
	OverwriteChoiceOverwrite OverwriteChoice = iota
	OverwriteChoiceSkip
	OverwriteChoiceOverwriteAll
	OverwriteChoiceKeepBoth
	OverwriteChoiceKeepBothAll
)

type YesNoChoice int

const (
	YesNoChoiceYes YesNoChoice = iota
	YesNoChoiceNo
)

// ConversionResult mirrors the C# ConversionResult sealed class.
type ConversionResult struct {
	FileName   string
	OutputText string
	Skipped    bool
	Dropped    bool // true if the file is neither valid ME nor SE and was rejected outright
	Warnings   []string
	Err        error
}

// ---------------------------------------------------------------------
// GUI mirrors the C# GUI : Control class.
// ---------------------------------------------------------------------

type GUI struct {
	mainWin *walk.MainWindow

	//btnMeToSe             *walk.PushButton
	btnSeToMe             *walk.PushButton
	btnBatchImportCreator *walk.PushButton
	teLog                 *walk.TextEdit

	lastLoadDir string
	lastSaveDir string

	isProcessing bool
	mu           sync.Mutex
}

func main() {
	gui := &GUI{}
	if err := gui.run(); err != nil {
		walk.MsgBox(nil, "Chyba", err.Error(), walk.MsgBoxIconError)
		os.Exit(1)
	}
}

func (g *GUI) run() error {
	if err := (MainWindow{
		AssignTo: &g.mainWin,
		Title:    "FactoryTalk View XML konverter SE do ME",
		MinSize:  Size{Width: 400, Height: 200},
		Size:     Size{Width: 600, Height: 300},
		Layout:   VBox{Margins: Margins{Left: 6, Top: 4, Right: 6, Bottom: 4}, Spacing: 4},
		Children: []Widget{
			Composite{
				Layout:  HBox{Margins: Margins{Left: 0, Top: 0, Right: 0, Bottom: 0}, Spacing: 6},
				MaxSize: Size{Width: 0, Height: 54},
				Children: []Widget{
					Label{
						Text:    "FTView XML konverter",
						Font:    Font{PointSize: 16, Bold: true},
						MaxSize: Size{Width: 0, Height: 0},
					},
					HSpacer{},
					/*PushButton{
						AssignTo: &g.btnMeToSe,
						Text:     "Konvertovat\r\nME -> SE",
						MinSize:  Size{Width: 160, Height: 50},
						OnClicked: func() {
							go g.processFilesAsync(ftviewconverter.MeToSe)
						},
					},*/
					PushButton{
						AssignTo:      &g.btnSeToMe,
						Text:          "Konvertovat\r\nSE -> ME",
						MinSize:       Size{Width: 100, Height: 50},
						MaxSize:       Size{Width: 100, Height: 50},
						StretchFactor: 0,
						OnClicked: func() {
							go g.processFilesAsync(ftviewconverter.SeToMe)
						},
					},
					PushButton{
						AssignTo:      &g.btnBatchImportCreator,
						Text:          "Vytvořit\r\nBatchImport.xml",
						MinSize:       Size{Width: 100, Height: 50},
						MaxSize:       Size{Width: 100, Height: 50},
						StretchFactor: 0,
						OnClicked: func() {
							go g.createBatchImportOnlyAsync()
						},
					},
				},
			},
			TextEdit{
				AssignTo: &g.teLog,
				ReadOnly: true,
				VScroll:  true,
				HScroll:  true,
			},
		},
	}).Create(); err != nil {
		return err
	}

	//makeButtonMultiline(g.btnMeToSe)
	makeButtonMultiline(g.btnSeToMe)
	makeButtonMultiline(g.btnBatchImportCreator)

	if icon, err := walk.NewIconFromFile("app.ico"); err == nil {
		g.mainWin.SetIcon(icon)
	}

	if brush, err := walk.NewSolidColorBrush(walk.RGB(255, 255, 255)); err == nil {
		g.teLog.SetBackground(brush)
	}

	g.mainWin.Run()
	return nil
}

// makeButtonMultiline adds the BS_MULTILINE style so embedded \r\n line
// breaks in a PushButton's text are actually rendered as line wraps.
// walk's declarative PushButton has no field for this, so it must be
// set directly on the underlying HWND after creation.
func makeButtonMultiline(btn *walk.PushButton) {
	hwnd := btn.Handle()
	style := win.GetWindowLong(hwnd, win.GWL_STYLE)
	win.SetWindowLong(hwnd, win.GWL_STYLE, style|win.BS_MULTILINE)
}

// ---------------------------------------------------------------------
// Logging helper. walk widgets must only be touched from the UI
// goroutine, so every log append and button-enable toggle is marshaled
// back via g.mainWin.Synchronize, mirroring how the C# version could
// safely mutate rtbLog.Text from the awaited continuation.
// ---------------------------------------------------------------------

func (g *GUI) log(format string, args ...interface{}) {
	line := toCRLF(fmt.Sprintf(format, args...))
	g.mainWin.Synchronize(func() {
		g.teLog.AppendText(line)
	})
}

func (g *GUI) setLog(text string) {
	text = toCRLF(text)
	g.mainWin.Synchronize(func() {
		g.teLog.SetText(text)
	})
}

// toCRLF normalizes bare \n line endings to \r\n, since the native
// TextEdit control only recognizes CRLF as a line break.
func toCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func (g *GUI) setButtonsEnabled(enabled bool) {
	g.mainWin.Synchronize(func() {
		//g.btnMeToSe.SetEnabled(enabled)
		g.btnSeToMe.SetEnabled(enabled)
		g.btnBatchImportCreator.SetEnabled(enabled)
	})
}

// ---------------------------------------------------------------------
// ProcessFilesAsync mirrors GUI.cs's ProcessFilesAsync.
// ---------------------------------------------------------------------

func (g *GUI) processFilesAsync(targetDirection ftviewconverter.Direction) {
	g.mu.Lock()
	if g.isProcessing {
		g.mu.Unlock()
		return
	}
	g.isProcessing = true
	g.mu.Unlock()

	g.setButtonsEnabled(false)
	defer func() {
		g.mu.Lock()
		g.isProcessing = false
		g.mu.Unlock()
		g.setButtonsEnabled(true)
	}()

	targetEd := "SE"
	sourceEd := "ME"
	if targetDirection == ftviewconverter.SeToMe {
		targetEd = "ME"
		sourceEd = "SE"
	}

	files := g.getFilesFromDialog(targetEd, "")
	if len(files) == 0 {
		return
	}

	g.setLog(fmt.Sprintf("Zahajování konverze do %s (%s -> %s)...\n", targetEd, sourceEd, targetEd))

	// Do the actual conversion work off the UI goroutine (this func
	// already runs on a background goroutine via `go` in run(), mirroring
	// C#'s Task.Run inside the async method).
	results := make([]ConversionResult, 0, len(files))
	converter := ftviewconverter.New()

	for _, file := range files {
		result := ConversionResult{FileName: filepath.Base(file)}

		xmlBytes, err := os.ReadFile(file)
		if err != nil {
			result.Err = err
			results = append(results, result)
			continue
		}
		xmlText := string(xmlBytes)

		currentEdition := converter.DetectEdition(xmlText)

		if currentEdition == ftviewconverter.EditionUnknown {
			// Not a recognizable ME or SE .gfx file at all - reject it
			// outright rather than risk running edition-specific
			// conversion rules against content that isn't either
			// dialect. Dropped entirely: no output written, not skipped
			// (skipped implies "already the target edition"), not
			// included in the save step or BatchImport.xml.
			result.Dropped = true
		} else if targetDirection == ftviewconverter.MeToSe && currentEdition == ftviewconverter.EditionSe {
			result.Skipped = true
			result.OutputText = xmlText
		} else if targetDirection == ftviewconverter.SeToMe && currentEdition == ftviewconverter.EditionMe {
			result.Skipped = true
			result.OutputText = xmlText
		} else {
			if targetDirection == ftviewconverter.MeToSe {
				result.OutputText = converter.ConvertMeToSe(xmlText)
			} else {
				result.OutputText = converter.ConvertSeToMe(xmlText)
			}
			result.Warnings = append(result.Warnings, converter.Warnings...)
		}

		results = append(results, result)
	}

	processedData := make(map[string]string)
	// Preserve encounter order of files for BatchImport.xml.
	order := make([]string, 0, len(results))

	for _, result := range results {
		g.log("Konvertování %s do %s\n", result.FileName, targetEd)

		if result.Err != nil {
			g.log("Chyba u %s - %s\n", result.FileName, result.Err.Error())
			continue
		}

		if result.Dropped {
			g.log("[X] %s není platný ME/SE soubor. Zahazuji...\n", result.FileName)
			continue
		}

		if result.Skipped {
			g.log("%s už je v edici %s. Přeskakuji...\n", result.FileName, targetEd)
		} else {
			for _, warning := range result.Warnings {
				g.log("[!] %s\n", warning)
			}
			g.log("%s konvertováno do %s\n", result.FileName, targetEd)
		}

		if result.OutputText != "" {
			processedData[result.FileName] = result.OutputText
			order = append(order, result.FileName)
		}
	}

	if len(processedData) == 0 {
		return
	}

	batchChoice := g.askYesNo(
		"Vytvořit Batch Import?",
		"Chcete vytvořit BatchImport.xml pro tyto soubory?\nSoubory budou uloženy do nové podsložky.",
	)
	createBatchImport := batchChoice == YesNoChoiceYes

	saveDir := g.getDirFromDialog(targetEd)
	if saveDir == "" {
		return
	}
	saveDir = normalizePath(saveDir)

	if createBatchImport {
		stamp := time.Now().Format("2006_01_02_15_04_05")
		folderName := fmt.Sprintf("%s_do_%s_%s", sourceEd, targetEd, stamp)
		saveDir = filepath.Join(saveDir, folderName)
		if err := os.MkdirAll(saveDir, 0o755); err != nil {
			g.log("Chyba při vytváření složky: %s - %s\n", saveDir, err.Error())
			return
		}
		g.log("Vytvořena složka: %s\n", saveDir)
	}

	overwriteAll := false
	keepBothAll := false
	savedFileNames := make([]string, 0, len(order))

	for _, fileName := range order {
		outputText := processedData[fileName]
		outPath := filepath.Join(saveDir, fileName)

		if !createBatchImport && fileExists(outPath) && !overwriteAll {
			if keepBothAll {
				outPath = makeTimestampedPath(outPath)
			} else {
				choice := g.askOverwrite(fileName)
				switch choice {
				case OverwriteChoiceOverwriteAll:
					overwriteAll = true
				case OverwriteChoiceSkip:
					g.log("Přeskočeno: %s (Soubor již existuje)\n", fileName)
					continue
				case OverwriteChoiceKeepBoth:
					outPath = makeTimestampedPath(outPath)
				case OverwriteChoiceKeepBothAll:
					keepBothAll = true
					outPath = makeTimestampedPath(outPath)
				}
			}
		}

		if err := os.WriteFile(outPath, []byte(outputText), 0o644); err != nil {
			g.log("Chyba ukládání: %s - %s\n", outPath, err.Error())
			continue
		}
		g.log("Uloženo: %s\n", outPath)
		savedFileNames = append(savedFileNames, filepath.Base(outPath))
	}

	if createBatchImport && len(savedFileNames) > 0 {
		batchImportPath := filepath.Join(saveDir, "BatchImport.xml")
		if err := writeBatchImportXml(batchImportPath, savedFileNames); err != nil {
			g.log("Chyba při vytváření BatchImport.xml - %s\n", err.Error())
		} else {
			g.log("Vytvořen BatchImport.xml: %s\n", batchImportPath)
		}
	}
}

// ---------------------------------------------------------------------
// CreateBatchImportOnlyAsync mirrors GUI.cs's CreateBatchImportOnlyAsync.
// ---------------------------------------------------------------------

func (g *GUI) createBatchImportOnlyAsync() {
	g.mu.Lock()
	if g.isProcessing {
		g.mu.Unlock()
		return
	}
	g.isProcessing = true
	g.mu.Unlock()

	g.setButtonsEnabled(false)
	defer func() {
		g.mu.Lock()
		g.isProcessing = false
		g.mu.Unlock()
		g.setButtonsEnabled(true)
	}()

	files := g.getFilesFromDialog("", "Vyberte soubory pro BatchImport.xml")
	if len(files) == 0 {
		return
	}

	g.setLog("Vytváření BatchImport.xml...\n")

	// Group selected files by directory, since BatchImport.xml must live
	// alongside the files it references and only lists plain filenames.
	byDirectory := make(map[string][]string)
	dirOrder := make([]string, 0)
	for _, file := range files {
		dir := normalizePath(filepath.Dir(file))
		if _, ok := byDirectory[dir]; !ok {
			dirOrder = append(dirOrder, dir)
		}
		byDirectory[dir] = append(byDirectory[dir], filepath.Base(file))
	}

	for _, dir := range dirOrder {
		fileNames := byDirectory[dir]
		batchImportPath := filepath.Join(dir, "BatchImport.xml")

		if fileExists(batchImportPath) {
			choice := g.askOverwrite("BatchImport.xml")
			switch choice {
			case OverwriteChoiceSkip:
				g.log("Přeskočeno: %s (Soubor již existuje)\n", batchImportPath)
				continue
			case OverwriteChoiceKeepBoth, OverwriteChoiceKeepBothAll:
				batchImportPath = makeTimestampedPath(batchImportPath)
			}
			// Overwrite / OverwriteAll: fall through and write to the original path.
		}

		if err := writeBatchImportXml(batchImportPath, fileNames); err != nil {
			g.log("Chyba při vytváření BatchImport.xml - %s\n", err.Error())
		} else {
			g.log("Vytvořen BatchImport.xml: %s\n", batchImportPath)
		}
	}
}

// ---------------------------------------------------------------------
// Dialog helpers. These run on a background goroutine and must
// synchronously block until the user responds, mirroring the C#
// TaskCompletionSource + DisplayServer.DialogShow pattern. walk's dialog
// APIs must be invoked on the UI goroutine, so each helper marshals the
// dialog creation via Synchronize and waits on a channel for the result.
// ---------------------------------------------------------------------

// askOverwrite mirrors AskOverwriteAsync. Button order matches the C#
// buttons array: [Přeskočit, Ponechat, Ponechat vše, Přepsat, Přepsat vše].
func (g *GUI) askOverwrite(fileName string) OverwriteChoice {
	resultCh := make(chan OverwriteChoice, 1)

	g.mainWin.Synchronize(func() {
		var dlg *walk.Dialog
		choice := OverwriteChoiceSkip

		_ = Dialog{
			AssignTo: &dlg,
			Title:    "Soubor již existuje",
			MinSize:  Size{Width: 420, Height: 160},
			Layout:   VBox{},
			Children: []Widget{
				Label{
					Text: fmt.Sprintf("Soubor '%s' již v cílové složce existuje.\nChcete jej přepsat?", fileName),
				},
				Composite{
					Layout: HBox{},
					Children: []Widget{
						HSpacer{},
						PushButton{
							Text: "Přeskočit",
							OnClicked: func() {
								choice = OverwriteChoiceSkip
								dlg.Accept()
							},
						},
						PushButton{
							Text: "Ponechat",
							OnClicked: func() {
								choice = OverwriteChoiceKeepBoth
								dlg.Accept()
							},
						},
						PushButton{
							Text: "Ponechat vše",
							OnClicked: func() {
								choice = OverwriteChoiceKeepBothAll
								dlg.Accept()
							},
						},
						PushButton{
							Text: "Přepsat",
							OnClicked: func() {
								choice = OverwriteChoiceOverwrite
								dlg.Accept()
							},
						},
						PushButton{
							Text: "Přepsat vše",
							OnClicked: func() {
								choice = OverwriteChoiceOverwriteAll
								dlg.Accept()
							},
						},
					},
				},
			},
		}.Create(g.mainWin)

		dlg.Run()
		resultCh <- choice
	})

	return <-resultCh
}

// askYesNo mirrors AskYesNoAsync. Button order matches the C# buttons
// array: [Ano, Ne].
func (g *GUI) askYesNo(title, message string) YesNoChoice {
	resultCh := make(chan YesNoChoice, 1)

	g.mainWin.Synchronize(func() {
		var dlg *walk.Dialog
		choice := YesNoChoiceNo

		_ = Dialog{
			AssignTo: &dlg,
			Title:    title,
			MinSize:  Size{Width: 420, Height: 140},
			Layout:   VBox{},
			Children: []Widget{
				Label{Text: message},
				Composite{
					Layout: HBox{},
					Children: []Widget{
						HSpacer{},
						PushButton{
							Text: "Ano",
							OnClicked: func() {
								choice = YesNoChoiceYes
								dlg.Accept()
							},
						},
						PushButton{
							Text: "Ne",
							OnClicked: func() {
								choice = YesNoChoiceNo
								dlg.Accept()
							},
						},
					},
				},
			},
		}.Create(g.mainWin)

		dlg.Run()
		resultCh <- choice
	})

	return <-resultCh
}

// getFilesFromDialog mirrors GetFilesFromDialogAsync. Returns nil if the
// user cancels.
func (g *GUI) getFilesFromDialog(targetEd, titleOverride string) []string {
	resultCh := make(chan []string, 1)

	title := titleOverride
	if title == "" {
		title = fmt.Sprintf("Vyberte soubory pro konverzi do %s", targetEd)
	}

	g.mainWin.Synchronize(func() {
		dlg := new(walk.FileDialog)
		dlg.Title = title
		dlg.Filter = "Soubory XML (*.xml)|*.xml"
		if g.lastLoadDir != "" {
			dlg.InitialDirPath = g.lastLoadDir
		}

		ok, err := dlg.ShowOpenMultiple(g.mainWin)
		if err != nil || !ok || len(dlg.FilePaths) == 0 {
			resultCh <- nil
			return
		}

		g.lastLoadDir = filepath.Dir(dlg.FilePaths[0])
		resultCh <- dlg.FilePaths
	})

	return <-resultCh
}

// getDirFromDialog mirrors GetDirFromDialogAsync. Returns "" if the user
// cancels.
func (g *GUI) getDirFromDialog(targetEd string) string {
	resultCh := make(chan string, 1)

	g.mainWin.Synchronize(func() {
		dlg := new(walk.FileDialog)
		dlg.Title = fmt.Sprintf("Vyberte složku pro uložení souborů %s", targetEd)
		if g.lastSaveDir != "" {
			dlg.InitialDirPath = g.lastSaveDir
		}

		ok, err := dlg.ShowBrowseFolder(g.mainWin)
		if err != nil || !ok || dlg.FilePath == "" {
			resultCh <- ""
			return
		}

		g.lastSaveDir = dlg.FilePath
		resultCh <- dlg.FilePath
	})

	return <-resultCh
}

// ---------------------------------------------------------------------
// Pure helpers mirroring static methods in GUI.cs.
// ---------------------------------------------------------------------

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func makeTimestampedPath(path string) string {
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	nameNoExt := strings.TrimSuffix(filepath.Base(path), ext)
	stamp := time.Now().Format("2006_01_02_15_04_05")
	return filepath.Join(dir, fmt.Sprintf("%s_%s%s", nameNoExt, stamp, ext))
}

func normalizePath(path string) string {
	return strings.ReplaceAll(path, "/", string(os.PathSeparator))
}

// writeBatchImportXml mirrors WriteBatchImportXml. FactoryTalk View
// expects UTF-16 LE with a BOM and no XML declaration, matching the C#
// implementation's use of UnicodeEncoding(bigEndian: false, BOM: true).
func writeBatchImportXml(path string, fileNames []string) error {
	var sb strings.Builder
	sb.WriteString("<gfxImport>\r\n")
	for _, fileName := range fileNames {
		escaped := escapeXMLAttribute(fileName)
		sb.WriteString(fmt.Sprintf("\t<import importFile=\"%s\"/>\r\n", escaped))
	}
	sb.WriteString("</gfxImport>\r\n")

	utf16Bytes := stringToUTF16LEWithBOM(sb.String())
	return os.WriteFile(path, utf16Bytes, 0o644)
}

// escapeXMLAttribute mirrors System.Security.SecurityElement.Escape for
// the characters that can appear in an XML attribute value.
func escapeXMLAttribute(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}

// stringToUTF16LEWithBOM encodes s as UTF-16 little-endian bytes prefixed
// with a byte-order mark, matching new UnicodeEncoding(false, true) in C#.
func stringToUTF16LEWithBOM(s string) []byte {
	codeUnits := utf16.Encode([]rune(s))
	out := make([]byte, 2+len(codeUnits)*2)
	// BOM: 0xFF 0xFE for UTF-16 LE.
	out[0] = 0xFF
	out[1] = 0xFE
	for i, u := range codeUnits {
		out[2+i*2] = byte(u)
		out[2+i*2+1] = byte(u >> 8)
	}
	return out
}
