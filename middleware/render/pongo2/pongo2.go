/*

   Copyright 2016 Wenhui Shen <www.webx.top>

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.

*/
package pongo2

import (
	"bytes"
	"io"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/admpub/log"
	. "github.com/admpub/pongo2"

	"github.com/webx-top/echo"
	"github.com/webx-top/echo/logger"
	"github.com/webx-top/echo/middleware/render"
	"github.com/webx-top/echo/middleware/render/driver"
	"github.com/webx-top/echo/middleware/render/manager"
)

func init() {
	render.Reg(`pongo2`, func(tmplDir string) driver.Driver {
		return New(tmplDir)
	})
}

func New(templateDir string, args ...logger.Logger) driver.Driver {
	var err error
	templateDir, err = filepath.Abs(templateDir)
	if err != nil {
		panic(err.Error())
	}
	a := &Pongo2{
		templateDir:       templateDir,
		ext:               `.html`,
		fileEvents:        make([]func(string), 0),
		contentProcessors: make([]func([]byte) []byte, 0),
	}
	if len(args) > 0 {
		a.logger = args[0]
	} else {
		a.logger = log.New("render-pongo2")
	}
	a.SetManager(manager.Default)
	return a
}

type Pongo2 struct {
	templates         map[string]*Template
	mutex             sync.RWMutex
	loader            *templateLoader
	set               *TemplateSet
	ext               string
	templateDir       string
	Mgr               driver.Manager
	logger            logger.Logger
	getFuncs          func() map[string]interface{}
	fileEvents        []func(string)
	contentProcessors []func([]byte) []byte
	debug             bool
	tmplPathFixer     func(echo.Context, string) string
}

type templateLoader struct {
	logger   logger.Logger
	template *Pongo2
}

func (a *templateLoader) Abs(base, name string) string {
	//a.logger.Info(base+" => %v\n", name)
	return filepath.Join(``, name)
}

// Get returns an io.Reader where the template's content can be read from.
func (a *templateLoader) Get(tmpl string) (io.Reader, error) {
	b, e := a.template.RawContent(tmpl + a.template.ext)
	if e != nil {
		a.logger.Error(e)
	}
	buf := new(bytes.Buffer)
	buf.Write(b)
	return buf, e
}

func (a *Pongo2) Debug() bool {
	return a.debug
}

func (a *Pongo2) SetDebug(on bool) {
	a.debug = on
}

func (a *Pongo2) SetLogger(l logger.Logger) {
	a.logger = l
	a.loader.logger = l
	if a.Mgr != nil {
		a.Mgr.SetLogger(a.logger)
	}
}
func (a *Pongo2) Logger() logger.Logger {
	return a.logger
}

func (a *Pongo2) TmplDir() string {
	return a.templateDir
}

func (a *Pongo2) SetTmplPathFixer(fn func(echo.Context, string) string) {
	a.tmplPathFixer = fn
}

func (a *Pongo2) TmplPath(c echo.Context, tmpl string) string {
	tmpl = strings.TrimPrefix(tmpl, a.templateDir)
	if a.tmplPathFixer != nil {
		tmpl = a.tmplPathFixer(c, tmpl)
	}
	return tmpl
}

func (a *Pongo2) MonitorEvent(fn func(string)) {
	if fn == nil {
		return
	}
	a.fileEvents = append(a.fileEvents, fn)
}

func (a *Pongo2) Init() {
	callback := func(name, typ, event string) {
		switch event {
		case "create":
		case "delete", "modify", "rename":
			if typ == "dir" || !strings.HasSuffix(name, a.ext) {
				return
			}
			key := strings.TrimSuffix(name, a.ext)
			//布局模板被修改时，清空缓存
			if strings.HasSuffix(key, `layout`) {
				a.templates = make(map[string]*Template)
				a.logger.Info(`remove all cached template object:`, name)
			} else if _, ok := a.templates[key]; ok {
				delete(a.templates, key)
				a.logger.Info(`remove cached template object:`, name)
			}
			for _, fn := range a.fileEvents {
				fn(name)
			}
		}
	}
	a.Mgr.AddAllow("*" + a.ext)
	a.Mgr.AddCallback(a.templateDir, callback)
	a.Mgr.AddWatchDir(a.templateDir)
	a.templates = map[string]*Template{}
	loader := &templateLoader{
		logger:   a.logger,
		template: a,
	}
	a.loader = loader
	a.set = NewSet(a.templateDir, a.loader)
	a.Mgr.Start()
}

func (a *Pongo2) SetManager(mgr driver.Manager) {
	if a.Mgr != nil {
		a.Mgr.Close()
	}
	a.Mgr = mgr
}

func (a *Pongo2) Manager() driver.Manager {
	return a.Mgr
}

func (a *Pongo2) SetContentProcessor(fn func([]byte) []byte) {
	if fn == nil {
		return
	}
	a.contentProcessors = append(a.contentProcessors, fn)
}

func (a *Pongo2) SetFuncMap(fn func() map[string]interface{}) {
	a.getFuncs = fn
}

func (a *Pongo2) Render(w io.Writer, tmpl string, data interface{}, c echo.Context) error {
	if c.Get(`webx:render.locked`) == nil {
		c.Set(`webx:render.locked`, true)
		a.mutex.Lock()
		defer func() {
			a.mutex.Unlock()
			c.Delete(`webx:render.locked`)
		}()
	}
	t, context := a.parse(c, tmpl, data)
	return t.ExecuteWriter(context, w)
}

func (a *Pongo2) parse(c echo.Context, tmpl string, data interface{}) (*Template, Context) {
	k := tmpl
	funcMap := c.Funcs()
	t, ok := a.templates[k]
	if !ok {
		var err error
		tmpl = a.TmplPath(c, tmpl)
		t, err = a.set.FromFile(tmpl)
		if err != nil {
			a.logger.Error(err)
			t = Must(a.set.FromString(err.Error()))
			return t, Context{}
		}
		a.templates[k] = t
	}
	var context Context
	if a.getFuncs != nil {
		context = Context(a.getFuncs())
	}
	if v, ok := data.(Context); ok {
		if context == nil {
			context = v
		} else {
			for n, f := range v {
				context[n] = f
			}
		}
	} else if v, ok := data.(map[string]interface{}); ok {
		if context == nil {
			context = v
		} else {
			for n, f := range v {
				context[n] = f
			}
		}
	} else {
		if context == nil {
			context = Context{
				`value`: data,
			}
		} else {
			context[`value`] = data
		}
	}
	if funcMap != nil {
		for name, function := range funcMap {
			context[name] = function
			//a.Logger.Info("added func: %v => %#v", name, function)
		}
	}
	return t, context
}

func (a *Pongo2) Fetch(tmpl string, data interface{}, c echo.Context) string {
	t, context := a.parse(c, tmpl, data)
	r, err := t.Execute(context)
	if err != nil {
		r = err.Error()
	}
	return r
}

var (
	ibRegex = regexp.MustCompile(`(?s)(\}|>)[\s]{2,}(\{|<})`)
)

func (a *Pongo2) RawContent(tmpl string) (b []byte, e error) {
	defer func() {
		if b != nil && a.contentProcessors != nil {
			for _, fn := range a.contentProcessors {
				b = fn(b)
			}
		}
		//b = Strip(b)
	}()
	if a.Mgr != nil {
		b, e = a.Mgr.GetTemplate(tmpl)
	}
	if b == nil || e != nil {
		b, e = ioutil.ReadFile(filepath.Join(a.templateDir, tmpl))
	}
	return
}

func Strip(b []byte) []byte {
	var pres [][]byte
	b, pres = driver.ReplacePRE(b)
	b = ibRegex.ReplaceAll(b, driver.FE)
	b = bytes.TrimSpace(b)
	b = driver.RecoveryPRE(b, pres)
	return b
}

func (a *Pongo2) ClearCache() {
	if a.Mgr != nil {
		a.Mgr.ClearCache()
	}
	a.templates = make(map[string]*Template)
}

func (a *Pongo2) Close() {
	a.ClearCache()
	if a.Mgr != nil {
		if a.Mgr == manager.Default {
			a.Mgr.CancelWatchDir(a.templateDir)
			a.Mgr.DelCallback(a.templateDir)
		} else {
			a.Mgr.Close()
		}
	}
}
