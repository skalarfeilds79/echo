/*

   Copyright 2016 Wenhui Shen <www.webx.top>

   Licensed under the Apache License, Version 2.0 (the `License`);
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an `AS IS` BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.

*/
package mvc

import (
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/webx-top/com"
	"github.com/webx-top/echo"
	"github.com/webx-top/echo/engine"
)

func NewContext(a *Application, c echo.Context) *Context {
	return &Context{
		Context:     c,
		Application: a,
	}
}

const (
	NO_PERM = -2 //无权限
	NO_AUTH = -1 //未登录
	FAILURE = 0  //操作失败
	SUCCESS = 1  //操作成功
)

type IniterFunc func(interface{}) error

type ContextInitial interface {
	Init(*Wrapper, interface{}, string) (error, bool)
}

type Context struct {
	echo.Context
	Application    *Application
	Module         *Module
	C              interface{}
	ControllerName string
	ActionName     string
	Tmpl           string

	exit bool
	body []byte
}

func (c *Context) Reset(req engine.Request, resp engine.Response) {
	c.Context.Reset(req, resp)

	c.ControllerName = ``
	c.Module = nil
	c.ActionName = ``
	c.Tmpl = ``
	c.Format()
	c.C = nil

	c.exit = false
	c.body = nil
}

func (c *Context) Init(wrp *Wrapper, controller interface{}, actName string) (error, bool) {
	c.Module = wrp.Module
	c.C = controller
	if c.Module.URLRecovery != nil {
		c.ControllerName = c.Module.URLRecovery(wrp.ControllerName)
		c.ActionName = c.Module.URLRecovery(actName)
	} else {
		c.ControllerName = wrp.ControllerName
		c.ActionName = actName
	}
	c.Tmpl = c.Module.Name + `/` + c.ControllerName + `/` + c.ActionName
	c.Context.SetRenderer(c.Module.Renderer)
	c.Context.SetSessionOptions(c.Application.SessionOptions)
	c.Context.SetFunc(`URLPath`, c.URLPath)
	c.Context.SetFunc(`BuildURL`, c.BuildURL)
	c.Context.SetFunc(`ModuleURLPath`, c.ModuleURLPath)
	c.Context.SetFunc(`ModuleURL`, c.ModuleURL)
	c.Context.SetFunc(`CheckPath`, c.CheckPath)
	c.Context.SetFunc(`ControllerName`, func() string {
		return c.ControllerName
	})
	c.Context.SetFunc(`ActionName`, func() string {
		return c.ActionName
	})
	c.Context.SetFunc(`ModuleName`, func() interface{} {
		return c.Module.Name
	})
	c.Context.SetFunc(`ModuleRoot`, func() string {
		return c.Module.URL
	})
	c.Context.SetFunc(`ModuleDomain`, func() string {
		return c.Module.Domain
	})
	c.Context.SetFunc(`C`, func() interface{} {
		return c.C
	})
	return nil, false
}

func (c *Context) SetSecCookie(key string, value interface{}) {
	if c.Application.Codec == nil {
		val, _ := value.(string)
		c.SetCookie(key, val)
		return
	}
	encoded, err := c.Application.Codec.Encode(key, value)
	if err != nil {
		c.Application.Core.Logger().Error(err)
	} else {
		c.SetCookie(key, encoded)
	}
}

func (c *Context) SecCookie(key string, value interface{}) {
	cookieValue := c.GetCookie(key)
	if len(cookieValue) == 0 {
		return
	}
	if c.Application.Codec != nil {
		err := c.Application.Codec.Decode(key, cookieValue, value)
		if err != nil {
			c.Application.Core.Logger().Error(err)
		}
		return
	}
	if v, ok := value.(*string); ok {
		*v = cookieValue
	}
}

func (c *Context) GetSecCookie(key string) (value string) {
	c.SecCookie(key, &value)
	return
}

func (c *Context) Body() ([]byte, error) {
	if c.body != nil {
		return c.body, nil
	}
	b := c.Request().Body()
	defer b.Close()
	body, err := ioutil.ReadAll(b)
	if err != nil {
		return nil, err
	}
	c.body = body
	return body, nil
}

func (c *Context) IP() string {
	return c.RealIP()
}

func (c *Context) OnlyAjax() bool {
	return c.IsAjax() && !c.IsPjax()
}

func (c *Context) PjaxContainer() string {
	return c.Header(`X-PJAX-Container`)
}

// Refer returns http referer header.
func (c *Context) Refer() string {
	return c.Referer()
}

// SubDomain returns sub domain string.
// if aa.bb.domain.com, returns aa.bb .
func (c *Context) SubDomain() string {
	parts := strings.Split(c.Host(), `.`)
	if len(parts) >= 3 {
		return strings.Join(parts[:len(parts)-2], `.`)
	}
	return ``
}

func (c *Context) Exit(args ...bool) *Context {
	exit := true
	if len(args) > 0 {
		exit = args[0]
	}
	c.exit = exit
	return c
}

func (c *Context) IsExit() bool {
	return c.exit
}

func (c *Context) CheckTmplPath(tpath string) string {
	if c.Module == nil {
		return tpath
	}
	if len(tpath) == 0 {
		return ``
	}
	switch tpath[0] {
	case '#': // 公共组件 {bin}/data/widgets
		tpath = filepath.Join(c.Module.Application.RootDir(), `data`, `widgets`, tpath[1:])
	case '@': // 模块模板
		tpath = tpath[1:]
		var tdir string
		if pos := strings.Index(tpath, `:`); pos > 0 {
			module := c.Module.Application.Module(tpath[0:pos])
			tpath = tpath[pos+1:]
			tdir = module.Renderer.TmplDir()
		} else {
			tdir = c.Module.Renderer.TmplDir()
		}
		tpath = filepath.Join(tdir, tpath)
	case '/': // 模块内模板
		tpath = c.Module.Name + tpath
	case ':': // 主题内模板
		tpath = filepath.Join(`..`, tpath[1:])
	default:
		tpath = c.Module.Name + `/` + c.ControllerName + `/` + tpath
	}
	return tpath
}

func (c *Context) Display(args ...interface{}) error {
	if c.Response().Committed() {
		return nil
	}
	switch len(args) {
	case 2:
		if v, ok := args[0].(string); ok {
			c.Tmpl = c.CheckTmplPath(v)
		}
		if v, ok := args[1].(int); ok && v > 0 {
			c.SetCode(v)
		}
	case 1:
		if v, ok := args[0].(int); ok {
			if v > 0 {
				c.SetCode(v)
			}
		} else if v, ok := args[0].(string); ok {
			c.Tmpl = c.CheckTmplPath(v)
		}
	}
	if c.Code() <= 0 {
		c.SetCode(c.Data().GetCode().HTTPCode())
	}
	if ignore, _ := c.Internal().Bool(`webx:ignoreRender`); ignore {
		return nil
	}
	return c.SetAuto(true).Render(c.Tmpl, nil)
}

// ErrorWithCode 生成HTTPError
func (c *Context) ErrorWithCode(code int, args ...string) *echo.HTTPError {
	return echo.NewHTTPError(code, args...)
}

// SetOutput 设置输出(code,info,zone,data)
func (c *Context) SetOutput(code int, args ...interface{}) *Context {
	c.Data().Set(code, args...)
	return c
}

// SetSuc 设置响应类型为“操作成功”(info,zone,data)
func (c *Context) SetSuc(args ...interface{}) *Context {
	return c.SetOutput(SUCCESS, args...)
}

// SetSucData 设置成功返回的数据
func (c *Context) SetSucData(data interface{}) *Context {
	return c.SetOutput(SUCCESS, ``, ``, data)
}

// SetErr 设置出错类型为“操作失败”(info,zone,data)
func (c *Context) SetErr(args ...interface{}) *Context {
	return c.SetOutput(FAILURE, args...)
}

// SetNoAuth 设置出错类型为“未登录”(info,zone,data)
func (c *Context) SetNoAuth(args ...interface{}) *Context {
	return c.SetOutput(NO_AUTH, args...)
}

// SetNoPerm 设置出错类型为“未授权”(message,for,data)
func (c *Context) SetNoPerm(args ...interface{}) *Context {
	return c.SetOutput(NO_PERM, args...)
}

// ModuleURLPath 生成指定Module网址
func (c *Context) ModuleURLPath(ppath string, args ...map[string]interface{}) string {
	return c.Application.URLBuilder.BuildFromPath(ppath, args...)
}

// RootModuleURL 生成根Module网址
func (c *Context) RootModuleURL(ctl string, act string, args ...interface{}) string {
	return c.Application.URLBuilder.Build(c.Module.RootModuleName, ctl, act, args...)
}

// ModuleURL 生成指定Module网址
func (c *Context) ModuleURL(mod string, ctl string, act string, args ...interface{}) string {
	return c.Application.URLBuilder.Build(mod, ctl, act, args...)
}

// URLFor ModuleURL的别名。生成指定Module网址
func (c *Context) URLFor(mod string, ctl string, act string, args ...interface{}) string {
	return c.Application.URLBuilder.Build(mod, ctl, act, args...)
}

// URLPath 生成当前Module网址
func (c *Context) URLPath(ppath string, args ...map[string]interface{}) string {
	if len(ppath) == 0 {
		if len(c.ControllerName) > 0 {
			ppath = c.ControllerName + `/`
		}
		ppath += c.ActionName
		return c.Application.URLBuilder.BuildFromPath(c.Module.Name+`/`+ppath, args...)
	}
	ppath = strings.TrimLeft(ppath, `/`)
	return c.Application.URLBuilder.BuildFromPath(c.Module.Name+`/`+ppath, args...)
}

// BuildURL 生成当前Module网址
func (c *Context) BuildURL(ctl string, act string, args ...interface{}) string {
	return c.Application.URLBuilder.Build(c.Module.Name, ctl, act, args...)
}

// TmplPath 生成模板路径 args: ActionName,ControllerName,ModuleName
func (c *Context) TmplPath(args ...string) string {
	var mod, ctl, act = c.Module.Name, c.ControllerName, c.ActionName
	switch len(args) {
	case 3:
		mod = args[2]
		fallthrough
	case 2:
		ctl = args[1]
		fallthrough
	case 1:
		act = args[0]
	}
	return mod + `/` + ctl + `/` + act
}

// SetTmpl 指定要渲染的模板路径
func (c *Context) SetTmpl(args ...string) *Context {
	c.Tmpl = c.TmplPath(args...)
	return c
}

// Atoe 字符串转error
func (c *Context) Atoe(v string) error {
	return errors.New(v)
}

// NextURL 获取下一步网址
func (c *Context) NextURL(defaultURL ...string) string {
	next := c.GetNextURL()
	if len(next) == 0 {
		next = c.U(defaultURL...)
	}
	return next
}

// GotoNext 跳转到下一步
func (c *Context) GotoNext(defaultURL ...string) error {
	return c.Go(c.NextURL(defaultURL...))
}

// GetNextURL 自动获取下一步网址
func (c *Context) GetNextURL(allowReferer ...bool) string {
	next := c.Header(`X-Next`)
	if len(next) == 0 {
		next = c.Form(`next`)
	}
	if len(next) > 0 {
		return c.ParseNextURL(next)
	}
	if len(allowReferer) < 1 || allowReferer[0] {
		next = c.Referer()
		if len(next) == 0 || !strings.HasSuffix(next, c.Request().URI()) {
			return next
		}
	}
	return ``
}

// ParseNextURL 解析下一步网址
func (c *Context) ParseNextURL(next string) string {
	if len(next) == 0 {
		return next
	}
	if next[0] == '!' {
		next = next[1:]
		var err error
		next, err = com.SafeBase64Decode(next)
		if err != nil {
			c.Application.Core.Logger().Error(err)
		}
	} else {
		next, _ = url.QueryUnescape(next)
	}
	return next
}

// GenNextURL 生成安全编码后的下一步网址
func (c *Context) GenNextURL(u string) string {
	if len(u) == 0 {
		return ``
	}
	if u[0] == '!' {
		return u
	}
	u = com.SafeBase64Encode(u)
	return `!` + u
}

// U 网址生成
// @usage:
// c.U(moduleName,controllerName,actionName) or c.U(moduleName,controllerName,actionName,`?q=coscms`)
// c.U(controllerName,actionName) or c.U(controllerName,actionName,`?q=coscms`)
// c.U(actionName) or  or c.U(actionName,`?q=coscms`)
func (c *Context) U(args ...string) (s string) {
	var p string
	switch len(args) {
	case 4:
		if args[3][0] == '?' {
			p = args[2]
		}
		fallthrough
	case 3:
		if len(p) > 0 || args[2][0] != '?' {
			return c.ModuleURL(args[0], args[1], args[2])
		}
		p = args[2]
		fallthrough
	case 2:
		if len(p) > 0 || args[1][0] != '?' {
			return c.BuildURL(args[0], args[1]) + p
		}
		p = args[1]
		fallthrough
	case 1:
		size := len(args[0])
		if len(p) > 0 || (size > 0 && args[0][0] != '?') {
			if size > 0 {
				switch args[0][0] {
				case '/': //usage: /webx/index => {module}/webx/index
					s = c.URLPath(args[0])
					return s + p
				case ':': //usage: :http://webx.top => http://webx.top
					s = args[0][1:]
					return s + p
				}
			}
			if strings.Contains(args[0], `/`) {
				s = c.ModuleURLPath(args[0])
			} else {
				s = c.ModuleURL(c.Module.Name, c.ControllerName, args[0])
			}
			return s + p
		}
		p = args[0]
		fallthrough
	case 0:
		s = c.ModuleURL(c.Module.Name, c.ControllerName, c.ActionName) + p
	}
	return
}

// Go 页面跳转
func (c *Context) Go(url string, args ...interface{}) error {
	var code = http.StatusFound //302. 307:http.StatusTemporaryRedirect
	if len(args) > 0 {
		if v, ok := args[0].(bool); ok && v {
			code = http.StatusMovedPermanently
		} else if v, ok := args[0].(int); ok {
			code = v
		}
	}
	c.Exit()
	if c.Format() != `html` || c.echoRedirect() {
		c.Internal().Set(`webx:ignoreRender`, false)
		c.Set(`Location`, url)
		return c.Display()
	}
	return c.Context.Redirect(url, code)
}

func (c *Context) echoRedirect() bool {
	format := c.Header(`X-Echo-Redirect`)
	if len(format) == 0 {
		return false
	}
	switch format {
	case `json`, `xml`:
		c.SetFormat(format)
		return true
	default:
		return false
	}
}

// Goto 页面跳转(根据路由生成网址后跳转)
func (c *Context) Goto(goURL string, args ...interface{}) error {
	goURL = c.U(goURL)
	return c.Go(goURL, args...)
}

// CheckPath 检查权限所用的网址路径
func (c *Context) CheckPath() string {
	if len(c.Module.Domain) > 0 {
		return `/` + c.Module.Name + c.Path()
	}
	return c.Path()
}

// Action 调用控制器方法
func (c *Context) Action(ctl string, act string) (err error) {
	a := c.Module.Wrapper(`controller.` + ctl)
	if a == nil {
		return c.Atoe(`Controller "` + ctl + `" does not exist.`)
	}
	k := `webx.controller.reflect.type:` + ctl
	var e reflect.Type
	if t, ok := c.Get(k).(reflect.Type); ok {
		e = t
	} else {
		e = reflect.Indirect(reflect.ValueOf(a.Controller)).Type()
		c.Set(k, e)
	}
	return a.Exec(c, e, act)
}

// ModuleAction 调用控制器方法
func (c *Context) ModuleAction(mod string, ctl string, act string) (err error) {
	module := c.Module.Application.Module(mod)
	a := module.Wrapper(`controller.` + ctl)
	if a == nil {
		return c.Atoe(`Controller "` + ctl + `" does not exist.`)
	}
	k := `webx.controller.reflect.type:` + ctl
	var e reflect.Type
	if t, ok := c.Get(k).(reflect.Type); ok {
		e = t
	} else {
		e = reflect.Indirect(reflect.ValueOf(a.Controller)).Type()
		c.Set(k, e)
	}
	return a.Exec(c, e, act)
}
