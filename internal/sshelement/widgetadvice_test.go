package sshelement

import (
	"regexp"
	"strings"
	"testing"
)

func TestWidgetAdviceSpotsWhatCannotWork(t *testing.T) {
	cases := map[string]string{
		`<script src="https://cdn.example/x.js"></script>`:      "<script src>",
		`<img src="https://x/y.png">`:                           "external image",
		`<link rel="stylesheet" href="a.css">`:                  "<link href>",
		`<style>@import "a.css";</style>`:                       "CSS cannot",
		`<style>b{background:url(https://x/y.png)}</style>`:     "CSS cannot",
		`<script>fetch("/x")</script>`:                          "no network",
		`<script>new XMLHttpRequest()</script>`:                 "no network",
		`<script>new WebSocket("ws://x")</script>`:              "no network",
		`<script type="module">import a from "./a.js"</script>`: "import cannot",
		`<script>await import("./a.js")</script>`:               "import cannot",
		`<script>localStorage.setItem("a",1)</script>`:          "no storage",
		`<form><input></form>`:                                  "form cannot",
		`<script>alert("hi")</script>`:                          "alert",
		`<script>window.open("x")</script>`:                     "popups",
		`<script>eval("1")</script>`:                            "eval",
		`<a href="https://x">go</a>`:                            "navigates",
		`<script>location.href="x"</script>`:                    "navigates",
		`<script>document.getElementById("v").textContent=1</script>
		 <div id="v"></div>`: `before the element "v"`,
		`<script>document.querySelector("#v").textContent=1</script><div id=v></div>`: `before the element "v"`,
		`<script>v.textContent=1</script><div id="v"></div>`:                          `before the element "v"`,
		`<script>document.getElementById("nope").textContent=1</script>`:              `no element has the id "nope"`,
	}
	for html, want := range cases {
		got := strings.Join(WidgetAdvice(html), "\n")
		if !strings.Contains(got, want) {
			t.Errorf("%s\n want %q, got %q", html, want, got)
		}
	}
}

func TestWidgetAdviceStaysQuietWhenItWorks(t *testing.T) {
	for _, html := range []string{
		`<div id="v"></div><script>document.getElementById("v").textContent=1</script>`,
		`<script>addEventListener("DOMContentLoaded",()=>{document.getElementById("v").textContent=1})</script><div id="v"></div>`,
		`<script>document.body.innerHTML='<div id="v"></div>';document.getElementById("v").textContent=1</script>`,
		`<!-- fetch("x") and <form> in a comment --><b>hi</b>`,
		`<a href="#top">up</a><img src="data:image/png;base64,AA">`,
		`<script>lola.ask("confirm this?");lola.run("prompt(1)");obj.confirm(1)</script>`,
		`<svg><circle id="c"/></svg><script>c.setAttribute("r",1)</script>`,
	} {
		if got := WidgetAdvice(html); len(got) != 0 {
			t.Errorf("%s\n warned: %q", html, got)
		}
	}
}

func TestWidgetAdviceIsBounded(t *testing.T) {
	var b strings.Builder
	for _, id := range strings.Split("a b c d e f g h i j k l", " ") {
		b.WriteString(`<script>document.getElementById("` + id + `")</script>`)
	}
	if got := WidgetAdvice(b.String()); len(got) != maxWidgetAdvice {
		t.Fatalf("%d lines", len(got))
	}
}

// What an agent is told to copy must pass its own checks, or the first thing it reads is a warning.
func TestWidgetGuideExamplesDrawNoAdvice(t *testing.T) {
	if got := WidgetAdvice(GuideWidgetTemplate); len(got) != 0 {
		t.Errorf("template: %q", got)
	}
	examples := regexp.MustCompile("(?s)```html (\\w+)\n(.*?)```").FindAllStringSubmatch(GuideWidgets, -1)
	if len(examples) < 5 {
		t.Fatalf("found %d examples", len(examples))
	}
	for _, m := range examples {
		if got := WidgetAdvice(m[2]); len(got) != 0 {
			t.Errorf("%s: %q", m[1], got)
		}
	}
	for _, want := range []string{"widget-template", "--data", "lola.view", "lola.rows", "lola.meter", "lola.spark", "lola.el", "[lola] Your widget"} {
		if !strings.Contains(GuideWidgets, want) {
			t.Errorf("guide does not mention %s", want)
		}
	}
}
