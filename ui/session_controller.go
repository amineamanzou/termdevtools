package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/gnocent/termdevtools/config"
	"github.com/gnocent/termdevtools/credentials"
	"github.com/gnocent/termdevtools/esclient"
	"github.com/gnocent/termdevtools/i18n"
	"github.com/rivo/tview"
)

type ControllerOptions struct {
	App          *tview.Application
	Config       *config.Config
	Inventory    config.Inventory
	ContextsPath string
	Paths        Paths
	Credentials  *credentials.Store
}
type sessionProfile struct {
	Identity, Name string
	Cluster        config.Cluster
	Definition     *config.ContextDefinition
	Files          config.ContextFiles
}
type sessionCandidate struct {
	Attempt    uint64
	Profile    sessionProfile
	Connection ConnectResult
	Work       workspace
	Values     credentials.Values
	Entered    []credentials.Field
}
type activeSession struct {
	profile    sessionProfile
	app        *App
	generation uint64
}
type SessionController struct {
	legacy                      *connectScreen
	legacyOpen                  bool
	options                     ControllerOptions
	inventory                   config.Inventory
	msgs                        *i18n.Strings
	ctx                         context.Context
	cancel                      context.CancelFunc
	done                        chan struct{}
	finishOnce                  sync.Once
	dispatcher                  *uiDispatcher
	root                        *tview.Flex
	pages                       *tview.Pages
	header                      *tview.TextView
	picker                      *contextPicker
	active                      *activeSession
	candidate                   *sessionCandidate
	candidateCancel             context.CancelFunc
	attempt, generation         uint64
	snapshots                   map[string]editorSnapshot
	pickerOpen, credentialsOpen bool
	previousFocus               tview.Primitive
	previousGeneration          uint64
	screen                      tcell.Screen
}

func NewSessionController(options ControllerOptions) *SessionController {
	if options.Credentials == nil {
		options.Credentials = credentials.NewStore(nil)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &SessionController{options: options, inventory: options.Inventory, msgs: i18n.For(options.Config.Language), ctx: ctx, cancel: cancel, done: make(chan struct{}), snapshots: map[string]editorSnapshot{}}
	c.dispatcher = newUIDispatcher(c.done)
	c.header = newContextHeader("", "")
	c.pages = tview.NewPages()
	c.picker = newContextPicker(options.App, c.msgs, c.choose, c.closePicker)
	c.pages.AddPage("contexts", c.picker.Widget(), true, false)
	c.root = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(c.header, 1, 0, false).AddItem(c.pages, 0, 1, true)
	return c
}
func (c *SessionController) Root() tview.Primitive { return c.root }
func (c *SessionController) start(initialContext string) error {
	if initialContext != "" {
		if _, ok := c.inventory.Lookup(initialContext); !ok {
			return fmt.Errorf(c.msgs.ContextUnknownFmt, initialContext)
		}
	}
	c.options.App.SetRoot(c.root, true).EnableMouse(c.options.Config.Mouse)
	c.options.App.SetInputCapture(c.handleKey)
	c.options.App.SetBeforeDrawFunc(c.dispatcher.BeforeDraw)
	c.options.App.SetAfterDrawFunc(func(screen tcell.Screen) {
		c.screen = screen
		if c.active != nil {
			c.active.app.BindScreen(screen)
		}
	})
	c.openPicker()
	if initialContext != "" {
		c.choose("named:" + initialContext)
	}
	return nil
}
func (c *SessionController) Run(initialContext string) error {
	defer c.finish()
	if err := c.start(initialContext); err != nil {
		return err
	}
	err := c.options.App.Run()
	if c.active != nil {
		c.active.app.client.CloseIdleConnections()
	}
	return err
}
func (c *SessionController) finish() { c.finishOnce.Do(func() { c.cancel(); close(c.done) }) }
func (c *SessionController) RequestStop(force bool) {
	c.dispatcher.Post(c.ctx, func() { c.stopUI(force) })
}
func (c *SessionController) stopUI(force bool) {
	c.cancelCandidate()
	if c.active != nil {
		a := c.active.app
		if err := a.SaveBeforeSwitch(); err != nil && !force && !a.exitWarned {
			a.exitWarned = true
			a.status.SetError(fmt.Sprintf(c.msgs.ErrExitSaveFailedFmt, err))
			c.picker.SetMessage(fmt.Sprintf(c.msgs.ErrExitSaveFailedFmt, err))
			return
		}
		a.lease.Close()
		a.client.CloseIdleConnections()
	}
	c.finish()
	c.options.App.Stop()
}
func (c *SessionController) entries() []contextEntry {
	entries := []contextEntry{}
	for _, d := range c.inventory.Definitions() {
		e := contextEntry{Identity: d.ID(), Name: d.Name, URL: d.Cluster.URL, Labels: d.Labels}
		if c.active != nil && c.active.profile.Identity == d.ID() {
			e.Active = true
			e.Changed = c.active.profile.Definition.Fingerprint() != d.Fingerprint()
		}
		entries = append(entries, e)
	}
	if c.active != nil && c.active.profile.Definition != nil {
		if _, ok := c.inventory.Lookup(c.active.profile.Name); !ok {
			d := c.active.profile.Definition
			entries = append(entries, contextEntry{Identity: d.ID(), Name: d.Name, URL: d.Cluster.URL, Labels: d.Labels, Active: true, Removed: true})
		}
	}
	if len(c.inventory.Definitions()) == 0 {
		for _, cl := range c.options.Config.Clusters {
			entries = append(entries, contextEntry{Identity: "legacy:" + cl.URL, Name: cl.URL, URL: cl.URL, Active: c.active != nil && c.active.profile.Identity == "legacy:"+cl.URL})
		}
		entries = append(entries, contextEntry{Identity: "action:new", Name: c.msgs.NewConnectionLabel, URL: c.msgs.NewConnectionSecondary})
	} else {
		entries = append(entries, contextEntry{Identity: "action:legacy", Name: c.msgs.ContextLegacyLabel, URL: c.msgs.ContextLegacySecondary})
	}
	return entries
}
func (c *SessionController) openPicker() {
	if c.pickerOpen && !c.credentialsOpen && !c.legacyOpen {
		c.picker.FocusFilter()
		return
	}
	if !c.pickerOpen {
		c.previousFocus = c.options.App.GetFocus()
		if c.legacyOpen {
			c.previousFocus = nil
		}
		c.previousGeneration = c.generation
	}
	c.cancelCandidate()
	c.pages.HidePage("legacy")
	c.legacyOpen = false

	c.pages.HidePage("credentials")
	c.pickerOpen = true
	c.credentialsOpen = false
	c.picker.SetEntries(c.entries())
	c.pages.ShowPage("contexts")
	c.picker.Open()
}
func (c *SessionController) closePicker() {
	c.cancelCandidate()
	c.credentialsOpen = false
	c.pages.HidePage("credentials")
	if c.active == nil {
		c.pages.ShowPage("contexts")
		c.pickerOpen = true
		c.picker.FocusFilter()
		return
	}
	c.pages.HidePage("contexts")
	c.pickerOpen = false
	if c.previousFocus != nil && c.previousGeneration == c.generation {
		c.options.App.SetFocus(c.previousFocus)
	} else {
		c.active.app.focusEditor()
	}
}
func (c *SessionController) cancelCandidate() {
	c.attempt++
	if c.candidateCancel != nil {
		c.candidateCancel()
		c.candidateCancel = nil
	}
	c.candidate = nil
}
func (c *SessionController) choose(identity string) {
	if identity == "action:legacy" {
		c.openLegacy()
		return
	}
	if identity == "action:new" {
		c.openLegacy()
		c.legacy.openForm(nil)
		return
	}
	if strings.HasPrefix(identity, "legacy:") {
		for _, cl := range c.options.Config.Clusters {
			if "legacy:"+cl.URL == identity {
				c.openLegacy()
				c.legacy.openForm(&cl)
				return
			}
		}
		return
	}
	if !strings.HasPrefix(identity, "named:") {
		return
	}
	d, ok := c.inventory.Lookup(strings.TrimPrefix(identity, "named:"))
	if !ok {
		c.picker.SetMessage(fmt.Sprintf(c.msgs.ContextUnknownFmt, identity))
		return
	}
	if c.active != nil && c.active.profile.Identity == identity && c.active.profile.Definition.Fingerprint() == d.Fingerprint() {
		c.closePicker()
		return
	}
	c.cancelCandidate()
	files, err := config.ContextWorkPaths(d.Name)
	if err != nil {
		c.picker.SetMessage(err.Error())
		return
	}
	candidate := &sessionCandidate{Attempt: c.attempt, Profile: sessionProfile{identity, d.Name, d.Cluster, &d, files}}
	c.candidate = candidate
	candidate.Values, _ = c.options.Credentials.Resolve(d, false)
	_, missing := c.options.Credentials.Resolve(d, false)
	if len(missing) > 0 {
		c.promptCredentials(candidate, missing)
	} else {
		c.validateCandidate(candidate)
	}
}
func (c *SessionController) promptCredentials(candidate *sessionCandidate, fields []credentials.Field) {
	c.credentialsOpen = true
	c.pages.HidePage("contexts")
	form := tview.NewForm().SetButtonsAlign(tview.AlignCenter)
	form.SetBorder(true).SetTitle(fmt.Sprintf(c.msgs.ContextCredentialTitleFmt, candidate.Profile.Name))
	entered := map[credentials.Field]string{}
	for _, field := range fields {
		field := field
		label := c.msgs.FieldPassword
		switch field {
		case credentials.FieldAPIKeyID:
			label = c.msgs.FieldAPIKeyID
		case credentials.FieldAPIKeySecret:
			label = c.msgs.FieldAPIKeySecret
		case credentials.FieldBearerToken:
			label = c.msgs.FieldBearerToken
		case credentials.FieldKeyPassphrase:
			label = c.msgs.FieldKeyPassphrase
		}
		change := func(value string) { entered[field] = value }
		if field == credentials.FieldAPIKeyID {
			form.AddInputField(label, "", 40, nil, change)
		} else {
			form.AddPasswordField(label, "", 40, '*', change)
		}
	}
	form.AddButton(c.msgs.ButtonConnect, func() {
		if c.candidate != candidate {
			return
		}
		for _, field := range fields {
			value := entered[field]
			if value == "" {
				return
			}
			switch field {
			case credentials.FieldPassword:
				candidate.Values.Password = value
			case credentials.FieldAPIKeyID:
				candidate.Values.APIKeyID = value
			case credentials.FieldAPIKeySecret:
				candidate.Values.APIKeySecret = value
			case credentials.FieldBearerToken:
				candidate.Values.BearerToken = value
			case credentials.FieldKeyPassphrase:
				candidate.Values.KeyPassphrase = value
			}
			candidate.Entered = append(candidate.Entered, field)
		}
		if candidate.Profile.Cluster.AuthType == config.AuthAPIKey {
			candidate.Values.APIKeyID, candidate.Values.APIKeySecret = esclient.ResolveAPIKey(candidate.Values.APIKeyID, candidate.Values.APIKeySecret)
			if candidate.Values.APIKeyID == "" {
				c.promptCredentials(candidate, []credentials.Field{credentials.FieldAPIKeyID})
				return
			}
		}
		c.credentialsOpen = false
		c.pages.HidePage("credentials")
		c.pages.ShowPage("contexts")
		c.validateCandidate(candidate)
	})
	form.AddButton(c.msgs.ButtonCancel, c.closePicker)
	form.SetCancelFunc(c.closePicker)
	c.pages.AddPage("credentials", form, true, true)
	c.options.App.SetFocus(form)
}
func (c *SessionController) validateCandidate(candidate *sessionCandidate) {
	ctx, cancel := context.WithCancel(c.ctx)
	c.candidateCancel = cancel
	profile, values, attempt := candidate.Profile, candidate.Values, candidate.Attempt
	var snapshot *editorSnapshot
	if c.active != nil && c.active.profile.Identity == profile.Identity {
		copy := c.active.app.Snapshot()
		snapshot = &copy
	} else if saved, ok := c.snapshots[profile.Identity]; ok {
		copy := saved
		snapshot = &copy
	}
	cheatsheet := c.options.Paths.Cheatsheet
	starter, timeout, msgs := c.options.Paths.Starter, time.Duration(c.options.Config.DefaultTimeoutSeconds)*time.Second, c.msgs
	connecting := fmt.Sprintf(msgs.ContextConnectingFmt, profile.Name)
	if proxy, err := esclient.ProxyFor(profile.Cluster.URL, profile.Cluster.Proxy); err == nil && proxy.URL != "" {
		connecting += " · " + fmt.Sprintf(msgs.StatusConnectingViaProxyFmt, proxy.URL, proxyOrigin(proxy))
	}
	c.picker.SetMessage(connecting)
	if profile.Definition == nil {
		c.legacy.setMessage(connecting, "yellow")
	}
	go func() {
		validation, stopValidation := context.WithTimeout(ctx, 15*time.Second)
		result, err := connectCluster(validation, profile.Cluster, values, timeout, msgs)
		stopValidation()
		var work workspace
		if err == nil {
			if profile.Definition == nil {
				work, err = prepareLegacyWorkspace(profile.Files, cheatsheet, starter, snapshot)
			} else {
				work, err = prepareWorkspace(profile.Files, starter, snapshot)
			}
		}
		var adopted atomic.Bool
		finished := make(chan struct{})
		defer func() {
			if result.Client != nil && !adopted.Load() {
				result.Client.CloseIdleConnections()
			}
		}()
		if !c.dispatcher.Post(ctx, func() {
			defer close(finished)
			if ctx.Err() != nil || c.candidate != candidate || c.attempt != attempt {
				return
			}
			if err != nil {
				if profile.Definition != nil && errors.Is(err, esclient.ErrKeyPassphraseRequired) {
					cancel()
					pass, missing := c.options.Credentials.Resolve(*profile.Definition, true)
					candidate.Values.KeyPassphrase = pass.KeyPassphrase
					if len(missing) > 0 {
						c.promptCredentials(candidate, missing)
					} else {
						c.validateCandidate(candidate)
					}
					return
				}
				var he *connectionHTTPError
				if errors.As(err, &he) && (he.StatusCode == 401 || he.StatusCode == 403) {
					c.options.Credentials.Forget(profile.Identity)
				}
				c.cancelCandidate()
				if profile.Definition == nil {
					c.legacy.setMessage(err.Error(), "red")
					c.legacyOpen = true
					c.pickerOpen = false
					c.options.App.SetFocus(c.legacy.pages)
					return
				}
				c.pages.ShowPage("contexts")
				c.credentialsOpen = false
				c.pickerOpen = true
				c.picker.SetMessage(err.Error())
				c.picker.FocusFilter()
				return
			}
			candidate.Connection = result
			candidate.Work = work
			if c.activate(candidate) {
				adopted.Store(true)
				c.cancelCandidate()
			}
		}) {
			return
		}
		select {
		case <-finished:
		case <-ctx.Done():
		case <-c.done:
		}
	}()
}
func (c *SessionController) activate(candidate *sessionCandidate) bool {
	generation := c.generation + 1
	lease := newSessionLease(c.ctx, candidate.Profile.Identity, generation, c.dispatcher, func() bool {
		return c.active != nil && c.active.generation == generation && c.active.profile.Identity == candidate.Profile.Identity
	})
	app := newSessionApp(c.options.App, candidate.Connection, c.options.Config, c.options.Paths, candidate.Work, lease)
	app.namedContext = candidate.Profile.Definition != nil
	if c.active != nil {
		if err := c.active.app.SaveBeforeSwitch(); err != nil {
			lease.Close()
			c.cancelCandidate()
			message := fmt.Sprintf(c.msgs.ContextSaveFailedFmt, err)
			c.picker.SetMessage(message)
			if candidate.Profile.Definition == nil {
				c.legacy.setMessage(message, "red")
			}
			return false
		}
		if c.active.profile.Identity == candidate.Profile.Identity {
			app.savedText = c.active.app.savedText
		}
		c.snapshots[c.active.profile.Identity] = c.active.app.Snapshot()
		c.active.app.lease.Close()
		c.active.app.client.CloseIdleConnections()
	}
	if candidate.Profile.Definition != nil {
		c.options.Credentials.Accept(*candidate.Profile.Definition, candidate.Values, candidate.Entered)
	}
	c.generation = generation
	c.active = &activeSession{candidate.Profile, app, generation}
	c.header.SetText(newContextHeader(candidate.Profile.Name, candidate.Connection.Target.Label()).GetText(false))
	c.pages.AddPage("session", app.Root(), true, true)
	c.pages.HidePage("contexts")
	c.pages.HidePage("credentials")
	c.pages.HidePage("legacy")
	c.legacyOpen = false
	c.pickerOpen = false
	c.credentialsOpen = false
	app.status.help.SetText(c.msgs.ContextHelpBar)
	app.helpView.SetText(c.msgs.ContextHelpContent + "\n\n" + strings.ReplaceAll(c.msgs.HelpContent, "F5/F6", "Ctrl+Shift+←/→"))
	if c.screen != nil {
		app.BindScreen(c.screen)
	}
	app.focusEditor()
	if candidate.Profile.Definition == nil {
		c.options.Config.Promote(candidate.Connection.Cluster)
		if err := c.options.Config.Save(); err != nil {
			app.status.SetWarning(fmt.Sprintf(c.msgs.WarnConnectedSaveFailedFmt, err))
		}
	}
	return true
}
func (c *SessionController) handleKey(event *tcell.EventKey) *tcell.EventKey {
	c.dispatcher.Drain()
	switch event.Key() {
	case tcell.KeyF61:
		return nil
	case tcell.KeyCtrlC:
		c.stopUI(false)
		return nil
	case tcell.KeyF7:
		c.reload()
		return nil
	case tcell.KeyF6:
		c.openPicker()
		return nil
	}
	if c.credentialsOpen || c.legacyOpen {
		if isExecuteShortcut(event) {
			return nil
		}
		return event
	}
	if c.pickerOpen {
		if isExecuteShortcut(event) || isCompletionShortcut(event) {
			return nil
		}
		return event
	}
	if c.active != nil {
		result := c.active.app.handleGlobalKeys(event)
		if c.active.app.msgs != c.msgs {
			c.msgs = c.active.app.msgs
			c.picker.SetLanguage(c.msgs)
			c.active.app.status.help.SetText(c.msgs.ContextHelpBar)
			c.active.app.helpView.SetText(c.msgs.ContextHelpContent + "\n\n" + strings.ReplaceAll(c.msgs.HelpContent, "F5/F6", "Ctrl+Shift+←/→"))
		}
		return result
	}
	return event
}

// reload updates declarations, never an active client. Invalid declarations
// leave the last valid inventory and its candidate untouched.
func (c *SessionController) reload() {
	var problems []error
	path := c.options.ContextsPath
	if path == "" {
		var err error
		path, err = config.ContextsPath()
		if err != nil {
			problems = append(problems, err)
		}
	}
	if path != "" {
		next, err := config.LoadContexts(path)
		if err != nil {
			problems = append(problems, err)
		} else {
			c.cancelCandidate()
			c.inventory = next
			c.options.Credentials.Reconcile(next)
			if c.credentialsOpen {
				c.credentialsOpen = false
				c.pages.HidePage("credentials")
				c.pages.ShowPage("contexts")
				c.pickerOpen = true
				c.picker.FocusFilter()
			}
		}
	}
	if c.active != nil {
		problems = append(problems, c.active.app.reloadSessionFiles()...)
	}
	c.picker.SetEntries(c.entries())
	if len(problems) > 0 {
		messages := make([]string, len(problems))
		for n, err := range problems {
			messages[n] = err.Error()
		}
		message := fmt.Sprintf(c.msgs.ContextReloadFailedFmt, strings.Join(messages, "\n"))
		c.picker.SetMessage(message)
		if c.active != nil {
			c.active.app.status.SetError(message)
		}
	} else {
		c.picker.SetMessage(c.msgs.ContextReloaded)
		if c.active != nil {
			c.active.app.status.SetInfo(c.msgs.ContextReloaded)
		}
	}
}

func (c *SessionController) openLegacy() {
	c.cancelCandidate()
	c.pickerOpen = false
	c.credentialsOpen = false
	c.legacyOpen = true
	c.pages.HidePage("contexts")
	c.pages.HidePage("credentials")
	c.legacy = newConnectScreen(c.options.App, c.options.Config, func(ConnectResult) {})
	c.legacy.attemptHandler = c.connectLegacy
	c.legacy.cancelHandler = c.cancelCandidate
	c.legacy.quitHandler = func() { c.stopUI(false) }
	c.pages.AddPage("legacy", c.legacy.pages, true, true)
	c.options.App.SetFocus(c.legacy.pages)
}
func (c *SessionController) connectLegacy(cl config.Cluster, secrets connectSecrets) {
	c.cancelCandidate()
	queries, err := config.QueriesPathForURL(cl.URL)
	if err != nil {
		c.legacy.setMessage(err.Error(), "red")
		return
	}
	vars, err := config.VariablesPathForURL(cl.URL)
	if err != nil {
		c.legacy.setMessage(err.Error(), "red")
		return
	}
	candidate := &sessionCandidate{Attempt: c.attempt, Profile: sessionProfile{Identity: "legacy:" + cl.URL, Name: cl.URL, Cluster: cl, Files: config.ContextFiles{Queries: queries, Variables: vars}}, Values: credentials.Values{Password: secrets.password, APIKeyID: cl.APIKeyID, APIKeySecret: secrets.apiKeySecret, BearerToken: secrets.bearerToken, KeyPassphrase: secrets.keyPassphrase}}
	c.candidate = candidate
	c.validateCandidate(candidate)
}
