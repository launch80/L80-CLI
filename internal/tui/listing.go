package tui

import (
	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// Buttons of the publish dialog. Their first letters are the dialog's
// shortcuts: l lists, p publishes unlisted, c cancels, so the key a person
// presses by habit for "publish" never makes a page public.
const (
	btnListPublish = "List & publish"
	btnUnlisted    = "Publish unlisted"
	btnPublish     = "Publish"
	btnCancel      = "Cancel"
)

// directoryNote is the part of the publish dialog that asks about the directory.
const directoryNote = "\n\nList it in the launch80 Directory (launch80.com/directory)? Anyone can find, " +
	"search and compare it there. Unlisted, only people you send the link to see it. " +
	"You can change it later in History (l)."

// confirmPublish asks before a publish. For a template the directory takes it
// also asks whether to list the page, unless the publish.public setting has
// answered already. Cancel is the default. then gets the listing choice.
func (c *Console) confirmPublish(body, template string, then func(public bool) Cmd) {
	if !api.Listable(template) {
		c.confirm(&widgets.Dialog{Title: "Publish", Body: body, Buttons: []string{btnPublish, btnCancel}, Choice: 1},
			func(ch int) Cmd {
				if ch == 0 {
					return then(false)
				}
				return nil
			})
		return
	}
	mode, _, _ := config.PublishListing()
	var buttons []string
	switch mode {
	case config.ListingAlways:
		body += "\n\nIt will be listed in the launch80 Directory (publish.public is always; change it in Settings)."
		buttons = []string{btnListPublish, btnCancel}
	case config.ListingNever:
		body += "\n\nIt will be unlisted: only people with the link see it (publish.public is never; change it in Settings)."
		buttons = []string{btnUnlisted, btnCancel}
	default:
		body += directoryNote
		buttons = []string{btnListPublish, btnUnlisted, btnCancel}
	}
	c.confirm(&widgets.Dialog{Title: "Publish", Body: body, Buttons: buttons, Choice: len(buttons) - 1},
		func(ch int) Cmd {
			if ch >= len(buttons)-1 {
				return nil
			}
			return then(buttons[ch] == btnListPublish)
		})
}

// publishedFlash is the flash after a publish: the URL, and where it is listed.
func publishedFlash(art *api.Artifact) string {
	msg := "published: " + art.URL
	if !api.Listable(art.TemplateID) {
		return msg
	}
	if art.Listed {
		return msg + " · listed in the Directory"
	}
	return msg + " · unlisted (History, l, to list it)"
}
