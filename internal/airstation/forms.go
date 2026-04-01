package airstation

import (
	"errors"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type Form struct {
	Method   string
	Action   string
	Controls []Control
}

type Control struct {
	Tag      string
	Name     string
	Type     string
	Value    string
	Checked  bool
	Disabled bool
	Options  []Option
}

type Option struct {
	Value    string
	Selected bool
}

func findForm(doc *goquery.Document, predicate func(*Form) bool) (*Form, error) {
	var matched *Form
	doc.Find("form").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		form := parseForm(selection)
		if !predicate(form) {
			return true
		}
		matched = form
		return false
	})
	if matched == nil {
		return nil, errors.New("matching form not found")
	}
	return matched, nil
}

func parseForm(selection *goquery.Selection) *Form {
	form := &Form{
		Method: strings.TrimSpace(selection.AttrOr("method", "POST")),
		Action: strings.TrimSpace(selection.AttrOr("action", "")),
	}

	selection.Find("input, textarea, select, button").Each(func(_ int, field *goquery.Selection) {
		tag := goquery.NodeName(field)
		control := Control{
			Tag:      strings.ToLower(tag),
			Name:     strings.TrimSpace(field.AttrOr("name", "")),
			Type:     strings.ToLower(strings.TrimSpace(field.AttrOr("type", ""))),
			Value:    field.AttrOr("value", ""),
			Checked:  field.Is("[checked]"),
			Disabled: field.Is("[disabled]"),
		}

		switch control.Tag {
		case "input":
			if (control.Type == "checkbox" || control.Type == "radio") && control.Value == "" {
				control.Value = "on"
			}
		case "textarea":
			control.Value = field.Text()
		case "select":
			control.Options = parseOptions(field)
		case "button":
			if control.Type == "" {
				control.Type = "submit"
			}
		}

		form.Controls = append(form.Controls, control)
	})

	return form
}

func parseOptions(selection *goquery.Selection) []Option {
	options := make([]Option, 0)
	selectedFound := false
	selection.Find("option").Each(func(_ int, option *goquery.Selection) {
		value, ok := option.Attr("value")
		if !ok {
			value = strings.TrimSpace(option.Text())
		}
		selected := option.Is("[selected]")
		selectedFound = selectedFound || selected
		options = append(options, Option{
			Value:    value,
			Selected: selected,
		})
	})

	if !selectedFound && len(options) > 0 {
		options[0].Selected = true
	}
	return options
}

func (f *Form) HasControl(name string) bool {
	for _, control := range f.Controls {
		if control.Name == name {
			return true
		}
	}
	return false
}

func (f *Form) DefaultValue(name string) (string, bool) {
	for _, control := range f.Controls {
		if control.Name != name {
			continue
		}
		if control.Value != "" {
			return control.Value, true
		}
		switch control.Type {
		case "checkbox", "radio":
			return "on", true
		default:
			return "", true
		}
	}
	return "", false
}

func (f *Form) Values(clickedName string) url.Values {
	values := url.Values{}
	for _, control := range f.Controls {
		if control.Disabled || control.Name == "" {
			continue
		}

		switch control.Tag {
		case "input":
			switch control.Type {
			case "checkbox", "radio":
				if control.Checked {
					values.Add(control.Name, control.Value)
				}
			case "submit", "image", "button":
				if control.Name == clickedName {
					values.Add(control.Name, control.Value)
				}
			case "reset", "file":
			default:
				values.Add(control.Name, control.Value)
			}
		case "textarea":
			values.Add(control.Name, control.Value)
		case "select":
			for _, option := range control.Options {
				if option.Selected {
					values.Add(control.Name, option.Value)
				}
			}
		case "button":
			if control.Type == "submit" && control.Name == clickedName {
				values.Add(control.Name, control.Value)
			}
		}
	}
	return values
}
