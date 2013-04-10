package gui

// import (
//   "github.com/runningwild/glop/gin"
//   "fmt"
//   "path/filepath"
//   "os"
//   "strings"
// )

// type FileWidget struct {
//   *Button
//   path   string
//   popup  Widget
//   choose *FileChooser

//   // Need to have a reference to the ui so that we can create a pop-up.  We can
//   // grab this on Think.
//   ui   *Gui
// }
// func (fw *FileWidget) GetPath() string {
//   return fw.path
// }
// func (fw *FileWidget) SetPath(path string) {
//   fw.path = path
//   fw.Button.SetText(filepath.Base(fw.path))
// }
// func (fw *FileWidget) Think(ui *Gui, t int64) {
//   fw.ui = ui
//   fw.Button.Think(ui, t)
// }
// func (fw *FileWidget) Respond(ui *Gui, group EventGroup) bool {
//   if found,event := group.FindEvent(gin.Escape); found && event.Type == gin.Press {
//     if fw.popup != nil {
//       fw.ui.DropFocus()
//       fw.ui.RemoveChild(fw.popup)
//       fw.popup = nil
//       return true
//     }
//   }

//   // By always returning true when in focus this essentially acts as a modal
//   // ui element.
//   if group.Focus {
//     fw.choose.Respond(ui, group)
//     return true
//   }

//   if fw.Button.Respond(ui, group) {
//     return true
//   }
//   cursor := group.Events[0].Key.Cursor()
//   if cursor == nil {
//     return false
//   }
//   var p Point
//   p.X, p.Y = cursor.Point()
//   v := p.Inside(fw.Rendered())
//   return v
// }

// // If path represents a directory, returns path
// // If path represents a file, returns the directory containing path
// // The path is always cleaned before it is returned
// // If there is an error stating path, "/" is returned
// func pathToDir(path string) string {
//   info,err := os.Stat(path)
//   if err != nil {
//     return "/"
//   }
//   if info.IsDir() {
//     return filepath.Clean(path)
//   }
//   return filepath.Clean(filepath.Join(path, ".."))
// }

// func MakeFileWidget(path string, filter func(string, bool) bool) *FileWidget {
//   var fw FileWidget
//   fw.path = path
//   fw.Button = MakeButton("standard", pathToDir(fw.path), 250, 1, 1, 1, 1, func(int64) {
//     anchor := MakeAnchorBox(fw.ui.root.Render_region.Dims)
//     callback := func(f string, err error) {
//       defer fw.ui.RemoveChild(anchor)
//       defer fw.ui.DropFocus()
//       fw.popup = nil
//       if err != nil { return }
//       fw.SetPath(f)
//     }
//     fw.choose = MakeFileChooser(pathToDir(fw.path), callback, filter)
//     anchor.AddChild(fw.choose, Anchor{ 0.5, 0.5, 0.5, 0.5 })
//     fw.popup = anchor
//     fw.ui.AddChild(fw.popup)
//     fw.ui.TakeFocus(&fw)
//   })
//   fw.SetPath(path)
//   return &fw
// }

// type FileChooser struct {
//   *VerticalTable
//   filename    *TextLine
//   up_button   *Button
//   list_scroll *ScrollFrame
//   list        *SelectBox
//   choose      *Button
//   callback    func(string, error)
//   filter      func(string, bool) bool
//   terminate   bool
// }

// func (fc *FileChooser) Respond(gui *Gui, group EventGroup) bool {
//   fc.VerticalTable.Respond(gui, group)
//   if found,event := group.FindEvent(gin.Escape); found && event.Type == gin.Press {
//     fc.terminate = true
//   }
//   return true
// }

// func (fc *FileChooser) Think(gui *Gui, t int64) {
//   fc.VerticalTable.Think(gui, t)
//   if fc.terminate {
//     fc.callback("", nil)
//   }
// }

// func (fc *FileChooser) setList() {
//   f,err := os.Open(fc.filename.GetText())
//   if err != nil {
//     fc.callback("", err)
//     return
//   }
//   defer f.Close()
//   infos,err := f.Readdir(0)
//   if err != nil {
//     fc.callback("", err)
//     return
//   }
//   var names []string
//   for _,info := range infos {
//     if fc.filter(info.Name(), info.IsDir()) {
//       names = append(names, info.Name())
//     }
//   }
//   nlist := MakeSelectTextBox(names, 300)
//   fc.list_scroll.ReplaceChild(fc.list, nlist)
//   fc.list = nlist
// }

// func (fc *FileChooser) up() {
//   path := fc.filename.GetText()
//   dir,file := filepath.Split(path)
//   if file == "" {
//     dir,file = filepath.Split(path[0 : len(path) - 1])
//   }
//   fc.filename.SetText(dir)
//   fc.setList()
// }

// type FileFilter func(string, bool) bool

// func MakeFileFilter(ext string) FileFilter {
//   return func(path string, is_dir bool) bool {
//     if is_dir { return true }
//     return strings.HasSuffix(path, ext)
//   }
// }

// func MakeFileChooser(dir string, callback func(string, error), filter func(string, bool) bool) *FileChooser {
//   var fc FileChooser
//   fc.callback = callback
//   fc.filter = filter
//   fc.filename = MakeTextLine("standard", dir, 300, 1, 1, 1, 1)
//   fmt.Printf("dir: %s\nother: %s\n", dir, fc.filename.GetText())
//   fc.up_button = MakeButton("standard", "Go up a directory", 200, 1, 1, 1, 1, func(int64) { 
//     fc.up()
//   })
//   fc.list = nil
//   fc.choose = MakeButton("standard", "Choose", 200, 1, 1, 1, 1, func(int64) {
//     next := filepath.Join(fc.filename.GetText(), fc.list.GetSelectedOption().(string))
//     f,err := os.Stat(next)
//     if err != nil {
//       callback("", err)
//       return
//     }
//     if f.IsDir() {
//       fc.filename.SetText(next)
//       fc.setList()
//     } else {
//       callback(next, nil)
//     }
//   })
//   fc.list_scroll = MakeScrollFrame(fc.list, 300, 300)
//   fc.VerticalTable = MakeVerticalTable()
//   fc.VerticalTable.AddChild(fc.filename)
//   fc.VerticalTable.AddChild(fc.up_button)
//   fc.VerticalTable.AddChild(fc.list_scroll)
//   fc.VerticalTable.AddChild(fc.choose)

//   fc.setList()
//   return &fc
// }

// func (w *FileChooser) String() string {
//   return "file chooser"
// }
