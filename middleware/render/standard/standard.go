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

/**
 * 模板扩展
 * @author swh <swh@admpub.com>
 */
package standard

import (
	"bytes"
	"fmt"
	htmlTpl "html/template"
	"io"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/admpub/log"

	"github.com/webx-top/echo"
	"github.com/webx-top/echo/logger"
	"github.com/webx-top/echo/middleware/render/driver"
	"github.com/webx-top/echo/middleware/render/manager"
)

var Debug = false

func New(templateDir string, args ...logger.Logger) driver.Driver {
	var err error
	templateDir, err = filepath.Abs(templateDir)
	if err != nil {
		panic(err.Error())
	}
	t := &Standard{
		CachedRelation:    make(map[string]*CcRel),
		TemplateDir:       templateDir,
		DelimLeft:         "{{",
		DelimRight:        "}}",
		IncludeTag:        "Include",
		FunctionTag:       "Function",
		ExtendTag:         "Extend",
		BlockTag:          "Block",
		SuperTag:          "Super",
		StripTag:          "Strip",
		Ext:               ".html",
		debug:             Debug,
		fileEvents:        make([]func(string), 0),
		contentProcessors: make([]func([]byte) []byte, 0),
	}
	if len(args) > 0 {
		t.logger = args[0]
	} else {
		t.logger = log.New("render-standard")
	}
	t.InitRegexp()
	t.SetManager(manager.Default)
	return t
}

type tplInfo struct {
	Template *htmlTpl.Template
	Blocks   map[string]struct{}
}

func NewTplInfo(t *htmlTpl.Template) *tplInfo {
	return &tplInfo{
		Template: t,
		Blocks:   map[string]struct{}{},
	}
}

type CcRel struct {
	Rel map[string]uint8
	Tpl [2]*tplInfo //0是独立模板；1是子模板
}

type Standard struct {
	CachedRelation     map[string]*CcRel
	TemplateDir        string
	TemplateMgr        driver.Manager
	contentProcessors  []func([]byte) []byte
	DelimLeft          string
	DelimRight         string
	incTagRegex        *regexp.Regexp
	funcTagRegex       *regexp.Regexp
	extTagRegex        *regexp.Regexp
	blkTagRegex        *regexp.Regexp
	rplTagRegex        *regexp.Regexp
	innerTagBlankRegex *regexp.Regexp
	stripTagRegex      *regexp.Regexp
	cachedRegexIdent   string
	IncludeTag         string
	FunctionTag        string
	ExtendTag          string
	BlockTag           string
	SuperTag           string
	StripTag           string
	Ext                string
	tmplPathFixer      func(echo.Context, string) string
	debug              bool
	getFuncs           func() map[string]interface{}
	logger             logger.Logger
	fileEvents         []func(string)
	mutex              sync.RWMutex
	quotedLeft         string
	quotedRight        string
	quotedRfirst       string
}

func (self *Standard) Debug() bool {
	return self.debug
}

func (self *Standard) SetDebug(on bool) {
	self.debug = on
}

func (self *Standard) SetLogger(l logger.Logger) {
	self.logger = l
	if self.TemplateMgr != nil {
		self.TemplateMgr.SetLogger(self.logger)
	}
}
func (self *Standard) Logger() logger.Logger {
	return self.logger
}

func (self *Standard) TmplDir() string {
	return self.TemplateDir
}

func (self *Standard) MonitorEvent(fn func(string)) {
	if fn == nil {
		return
	}
	self.fileEvents = append(self.fileEvents, fn)
}

func (self *Standard) SetContentProcessor(fn func([]byte) []byte) {
	if fn == nil {
		return
	}
	self.contentProcessors = append(self.contentProcessors, fn)
}

func (self *Standard) SetFuncMap(fn func() map[string]interface{}) {
	self.getFuncs = fn
}

func (self *Standard) deleteCachedRelation(name string) {
	if cs, ok := self.CachedRelation[name]; ok {
		_ = cs
		self.CachedRelation = make(map[string]*CcRel)
		self.logger.Info("remove cached template object")
		/*
			for key, _ := range cs.Rel {
				if key == name {
					continue
				}
				self.deleteCachedRelation(key)
			}
			self.Logger.Info("remove cached template object:", name)
			delete(self.CachedRelation, name)
		*/
	}
}

func (self *Standard) Init() {
	self.InitRegexp()
	callback := func(name, typ, event string) {
		switch event {
		case "create":
		case "delete", "modify", "rename":
			if typ == "dir" {
				return
			}
			self.deleteCachedRelation(name)
			for _, fn := range self.fileEvents {
				fn(name)
			}
		}
	}
	self.TemplateMgr.AddAllow("*" + self.Ext)
	self.TemplateMgr.AddWatchDir(self.TemplateDir)
	self.TemplateMgr.AddCallback(self.TemplateDir, callback)
	self.TemplateMgr.Start()
}

func (self *Standard) SetManager(mgr driver.Manager) {
	if self.TemplateMgr != nil {
		self.TemplateMgr.Close()
	}
	self.TemplateMgr = mgr
}

func (self *Standard) Manager() driver.Manager {
	return self.TemplateMgr
}

func (self *Standard) SetTmplPathFixer(fn func(echo.Context, string) string) {
	self.tmplPathFixer = fn
}

func (self *Standard) TmplPath(c echo.Context, p string) string {
	if self.tmplPathFixer != nil {
		return self.tmplPathFixer(c, p)
	}
	p = filepath.Join(self.TemplateDir, p)
	return p
}

func (self *Standard) InitRegexp() {
	self.quotedLeft = regexp.QuoteMeta(self.DelimLeft)
	self.quotedRight = regexp.QuoteMeta(self.DelimRight)
	self.quotedRfirst = regexp.QuoteMeta(self.DelimRight[0:1])

	//{{Include "tmpl"}} or {{Include "tmpl" .}}
	self.incTagRegex = regexp.MustCompile(self.quotedLeft + self.IncludeTag + `[\s]+"([^"]+)"(?:[\s]+([^` + self.quotedRfirst + `]+))?[\s]*\/?` + self.quotedRight)

	//{{Function "funcName"}} or {{Function "funcName" .}}
	self.funcTagRegex = regexp.MustCompile(self.quotedLeft + self.FunctionTag + `[\s]+"([^"]+)"(?:[\s]+([^` + self.quotedRfirst + `]+))?[\s]*\/?` + self.quotedRight)

	//{{Extend "name"}}
	self.extTagRegex = regexp.MustCompile(`^[\s]*` + self.quotedLeft + self.ExtendTag + `[\s]+"([^"]+)"(?:[\s]+([^` + self.quotedRfirst + `]+))?[\s]*\/?` + self.quotedRight)

	//{{Block "name"}}content{{/Block}}
	self.blkTagRegex = regexp.MustCompile(`(?s)` + self.quotedLeft + self.BlockTag + `[\s]+"([^"]+)"[\s]*` + self.quotedRight + `(.*?)` + self.quotedLeft + `\/` + self.BlockTag + self.quotedRight)

	//{{Block "name"/}}
	self.rplTagRegex = regexp.MustCompile(self.quotedLeft + self.BlockTag + `[\s]+"([^"]+)"[\s]*\/` + self.quotedRight)

	//}}...{{ or >...<
	self.innerTagBlankRegex = regexp.MustCompile(`(?s)(` + self.quotedRight + `|>)[\s]{2,}(` + self.quotedLeft + `|<)`)

	//{{Strip}}...{{/Strip}}
	self.stripTagRegex = regexp.MustCompile(`(?s)` + self.quotedLeft + self.StripTag + self.quotedRight + `(.*?)` + self.quotedLeft + `\/` + self.StripTag + self.quotedRight)
}

// Render HTML
func (self *Standard) Render(w io.Writer, tmplName string, values interface{}, c echo.Context) error {
	if c.Get(`webx:render.locked`) == nil {
		c.Set(`webx:render.locked`, true)
		self.mutex.Lock()
		defer func() {
			self.mutex.Unlock()
			c.Delete(`webx:render.locked`)
		}()
	}
	tmpl, err := self.parse(c, tmplName)
	if err != nil {
		return err
	}
	return tmpl.ExecuteTemplate(w, tmpl.Name(), values)
}

func (self *Standard) parse(c echo.Context, tmplName string) (tmpl *htmlTpl.Template, err error) {
	funcs := c.Funcs()
	tmplOriginalName := tmplName
	tmplName = tmplName + self.Ext
	tmplName = self.TmplPath(c, tmplName)
	cachedKey := tmplName
	var funcMap htmlTpl.FuncMap
	if self.getFuncs != nil {
		funcMap = htmlTpl.FuncMap(self.getFuncs())
	}
	if funcMap == nil {
		funcMap = htmlTpl.FuncMap{}
	}
	for k, v := range funcs {
		funcMap[k] = v
	}
	rel, ok := self.CachedRelation[cachedKey]
	if ok && rel.Tpl[0].Template != nil {
		tmpl = rel.Tpl[0].Template
		funcMap = setFunc(rel.Tpl[0], funcMap)
		tmpl.Funcs(funcMap)
		return
	}
	if self.debug {
		start := time.Now()
		self.logger.Debug(` ◐ compile template: `, tmplName)
		defer func() {
			self.logger.Debug(` ◑ finished compile: `+tmplName, ` (elapsed: `+time.Now().Sub(start).String()+`)`)
		}()
	}
	t := htmlTpl.New(driver.CleanTemplateName(tmplName))
	t.Delims(self.DelimLeft, self.DelimRight)
	if rel == nil {
		rel = &CcRel{
			Rel: map[string]uint8{cachedKey: 0},
			Tpl: [2]*tplInfo{NewTplInfo(nil), NewTplInfo(nil)},
		}
	}
	funcMap = setFunc(rel.Tpl[0], funcMap)
	t.Funcs(funcMap)
	var b []byte
	b, err = self.RawContent(tmplName)
	if err != nil {
		tmpl, _ = t.Parse(err.Error())
		return
	}
	content := string(b)
	subcs := make(map[string]string, 0) //子模板内容
	extcs := make(map[string]string, 0) //母板内容
	m := self.extTagRegex.FindAllStringSubmatch(content, 1)
	content = self.rplTagRegex.ReplaceAllString(content, ``)
	for i := 0; i < 10 && len(m) > 0; i++ {
		self.ParseBlock(c, content, subcs, extcs)
		extFile := m[0][1] + self.Ext
		passObject := m[0][2]
		extFile = self.TmplPath(c, extFile)
		b, err = self.RawContent(extFile)
		if err != nil {
			tmpl, _ = t.Parse(err.Error())
			return
		}
		content = string(b)
		content, m = self.ParseExtend(c, content, extcs, passObject, subcs)

		if v, ok := self.CachedRelation[extFile]; !ok {
			self.CachedRelation[extFile] = &CcRel{
				Rel: map[string]uint8{cachedKey: 0},
				Tpl: [2]*tplInfo{NewTplInfo(nil), NewTplInfo(nil)},
			}
		} else if _, ok := v.Rel[cachedKey]; !ok {
			self.CachedRelation[extFile].Rel[cachedKey] = 0
		}
	}
	content = self.ContainsSubTpl(c, content, subcs)
	clips := map[string]string{}
	content = self.ContainsFunctionResult(c, tmplOriginalName, content, clips)
	tmpl, err = t.Parse(content)
	if err != nil {
		content = fmt.Sprintf("Parse %v err: %v", tmplName, err)
		tmpl, _ = t.Parse(content)
		return
	}
	for name, subc := range subcs {
		v, ok := self.CachedRelation[name]
		if ok && v.Tpl[1].Template != nil {
			self.CachedRelation[name].Rel[cachedKey] = 0
			tmpl.AddParseTree(name, self.CachedRelation[name].Tpl[1].Template.Tree)
			continue
		}
		var t *htmlTpl.Template
		if name == tmpl.Name() {
			t = tmpl
		} else {
			subc = self.ContainsFunctionResult(c, tmplOriginalName, subc, clips)
			t = tmpl.New(name)
			subc = self.Tag(`define "`+driver.CleanTemplateName(name)+`"`) + subc + self.Tag(`end`)
			_, err = t.Parse(subc)
			if err != nil {
				t.Parse(fmt.Sprintf("Parse File %v err: %v", name, err))
				return
			}
		}

		if ok {
			self.CachedRelation[name].Rel[cachedKey] = 0
			self.CachedRelation[name].Tpl[1].Template = t
		} else {
			self.CachedRelation[name] = &CcRel{
				Rel: map[string]uint8{cachedKey: 0},
				Tpl: [2]*tplInfo{NewTplInfo(nil), NewTplInfo(t)},
			}
		}

	}
	for name, extc := range extcs {
		var t *htmlTpl.Template
		if name == tmpl.Name() {
			t = tmpl
		} else {
			t = tmpl.New(name)
			extc = self.ContainsFunctionResult(c, tmplOriginalName, extc, clips)
			extc = self.Tag(`define "`+driver.CleanTemplateName(name)+`"`) + extc + self.Tag(`end`)
			_, err = t.Parse(extc)
			if err != nil {
				t.Parse(fmt.Sprintf("Parse Block %v err: %v", name, err))
				return
			}
		}
		rel.Tpl[0].Blocks[name] = struct{}{}
	}

	rel.Tpl[0].Template = tmpl
	self.CachedRelation[cachedKey] = rel
	return
}

func (self *Standard) Fetch(tmplName string, data interface{}, c echo.Context) string {
	content, _ := self.parse(c, tmplName)
	return self.execute(content, data)
}

func (self *Standard) execute(tmpl *htmlTpl.Template, data interface{}) string {
	buf := new(bytes.Buffer)
	err := tmpl.ExecuteTemplate(buf, tmpl.Name(), data)
	if err != nil {
		return fmt.Sprintf("Parse %v err: %v", tmpl.Name(), err)
	}
	return buf.String()
}

func (self *Standard) ParseBlock(c echo.Context, content string, subcs map[string]string, extcs map[string]string) {
	matches := self.blkTagRegex.FindAllStringSubmatch(content, -1)
	for _, v := range matches {
		blockName := v[1]
		content := v[2]
		extcs[blockName] = self.ContainsSubTpl(c, content, subcs)
	}
}

func (self *Standard) ParseExtend(c echo.Context, content string, extcs map[string]string, passObject string, subcs map[string]string) (string, [][]string) {
	m := self.extTagRegex.FindAllStringSubmatch(content, 1)
	hasParent := len(m) > 0
	if len(passObject) == 0 {
		passObject = "."
	}
	content = self.rplTagRegex.ReplaceAllStringFunc(content, func(match string) string {
		match = match[strings.Index(match, `"`)+1:]
		match = match[0:strings.Index(match, `"`)]
		if v, ok := extcs[match]; ok {
			return v
		}
		return ``
	})
	matches := self.blkTagRegex.FindAllStringSubmatch(content, -1)
	var superTag string
	if len(self.SuperTag) > 0 {
		superTag = self.Tag(self.SuperTag)
	}
	rec := make(map[string]uint8)
	sup := make(map[string]string)
	for _, v := range matches {
		matched := v[0]
		blockName := v[1]
		innerStr := v[2]
		if v, ok := extcs[blockName]; ok {
			var suffix string
			if idx, ok := rec[blockName]; ok {
				idx++
				rec[blockName] = idx
				suffix = fmt.Sprintf(`.%v`, idx)
			} else {
				rec[blockName] = 0
			}
			if len(superTag) > 0 {
				sv, hasSuper := sup[blockName]
				if !hasSuper {
					hasSuper = strings.Contains(v, superTag)
					if hasSuper {
						sup[blockName] = v
					}
				} else {
					v = sv
				}
				if hasSuper {
					innerStr = self.ContainsSubTpl(c, innerStr, subcs)
					v = strings.Replace(v, superTag, innerStr, 1)
					if suffix == `` {
						extcs[blockName] = v
					}
				}
			}
			if len(suffix) > 0 {
				extcs[blockName+suffix] = v
				rec[blockName+suffix] = 0
			}
			if hasParent {
				content = strings.Replace(content, matched, self.DelimLeft+self.BlockTag+` "`+blockName+`"`+self.DelimRight+v+self.DelimLeft+`/`+self.BlockTag+self.DelimRight, 1)
			} else {
				content = strings.Replace(content, matched, self.Tag(`template "`+blockName+suffix+`" `+passObject), 1)
			}
		} else {
			if !hasParent {
				content = strings.Replace(content, matched, innerStr, 1)
			}
		}
	}
	//只保留layout中存在的Block
	for k := range extcs {
		if _, ok := rec[k]; !ok {
			delete(extcs, k)
		}
	}
	return content, m
}

func (self *Standard) ContainsSubTpl(c echo.Context, content string, subcs map[string]string) string {
	matches := self.incTagRegex.FindAllStringSubmatch(content, -1)
	for _, v := range matches {
		matched := v[0]
		tmplFile := v[1]
		passObject := v[2]
		tmplFile += self.Ext
		tmplFile = self.TmplPath(c, tmplFile)
		if _, ok := subcs[tmplFile]; !ok {
			// if v, ok := self.CachedRelation[tmplFile]; ok && v.Tpl[1] != nil {
			// 	subcs[tmplFile] = ""
			// } else {
			b, err := self.RawContent(tmplFile)
			if err != nil {
				return fmt.Sprintf("RenderTemplate %v read err: %s", tmplFile, err)
			}
			str := string(b)
			subcs[tmplFile] = "" //先登记，避免死循环
			str = self.ContainsSubTpl(c, str, subcs)
			subcs[tmplFile] = str
			//}
		}
		if len(passObject) == 0 {
			passObject = "."
		}
		content = strings.Replace(content, matched, self.Tag(`template "`+driver.CleanTemplateName(tmplFile)+`" `+passObject), -1)
	}
	return content
}

func (self *Standard) ContainsFunctionResult(c echo.Context, tmplOriginalName string, content string, clips map[string]string) string {
	matches := self.funcTagRegex.FindAllStringSubmatch(content, -1)
	for _, v := range matches {
		matched := v[0]
		funcName := v[1]
		passArg := v[2]
		key := funcName + `:` + passArg
		if _, ok := clips[key]; !ok {
			if fn, ok := c.GetFunc(funcName).(func(string, string) string); ok {
				clips[key] = fn(tmplOriginalName, passArg)
			} else {
				clips[key] = ``
			}
		}

		content = strings.Replace(content, matched, clips[key], -1)
	}
	return content
}

func (self *Standard) Tag(content string) string {
	return self.DelimLeft + content + self.DelimRight
}

func (self *Standard) preprocess(b []byte) []byte {
	if b == nil {
		return nil
	}
	if self.contentProcessors != nil {
		for _, fn := range self.contentProcessors {
			b = fn(b)
		}
	}
	return self.strip(b)
}

func (self *Standard) RawContent(tmpl string) (b []byte, e error) {
	if self.TemplateMgr != nil {
		b, e = self.TemplateMgr.GetTemplate(tmpl)
	} else {
		b, e = ioutil.ReadFile(tmpl)
	}
	if e != nil {
		return
	}
	b = self.preprocess(b)
	return
}

func (self *Standard) strip(src []byte) []byte {
	if self.debug {
		src = bytes.ReplaceAll(src, []byte(self.DelimLeft+self.StripTag+self.DelimRight), []byte{})
		return bytes.ReplaceAll(src, []byte(self.DelimLeft+`/`+self.StripTag+self.DelimRight), []byte{})
	}
	src = self.stripTagRegex.ReplaceAllFunc(src, func(b []byte) []byte {
		b = bytes.TrimPrefix(b, []byte(self.DelimLeft+self.StripTag+self.DelimRight))
		b = bytes.TrimSuffix(b, []byte(self.DelimLeft+`/`+self.StripTag+self.DelimRight))
		var pres [][]byte
		b, pres = driver.ReplacePRE(b)
		b = self.innerTagBlankRegex.ReplaceAll(b, driver.FE)
		b = driver.RemoveMultiCRLF(b)
		b = bytes.TrimSpace(b)
		b = driver.RecoveryPRE(b, pres)
		return b
	})
	return src
}

func (self *Standard) stripSpace(b []byte) []byte {
	var pres [][]byte
	b, pres = driver.ReplacePRE(b)
	b = self.innerTagBlankRegex.ReplaceAll(b, driver.FE)
	b = bytes.TrimSpace(b)
	b = driver.RecoveryPRE(b, pres)
	return b
}

func (self *Standard) ClearCache() {
	if self.TemplateMgr != nil {
		self.TemplateMgr.ClearCache()
	}
	self.CachedRelation = make(map[string]*CcRel)
}

func (self *Standard) Close() {
	self.ClearCache()
	if self.TemplateMgr != nil {
		if self.TemplateMgr == manager.Default {
			self.TemplateMgr.CancelWatchDir(self.TemplateDir)
			self.TemplateMgr.DelCallback(self.TemplateDir)
		} else {
			self.TemplateMgr.Close()
		}
	}
}

func setFunc(tplInf *tplInfo, funcMap htmlTpl.FuncMap) htmlTpl.FuncMap {
	if funcMap == nil {
		funcMap = htmlTpl.FuncMap{}
	}
	funcMap["hasBlock"] = func(blocks ...string) bool {
		for _, blockName := range blocks {
			if _, ok := tplInf.Blocks[blockName]; !ok {
				return false
			}
		}
		return true
	}
	funcMap["hasAnyBlock"] = func(blocks ...string) bool {
		for _, blockName := range blocks {
			if _, ok := tplInf.Blocks[blockName]; ok {
				return true
			}
		}
		return false
	}
	return funcMap
}
