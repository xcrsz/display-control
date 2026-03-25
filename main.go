// main.go — Display Control with evidence-based circadian features
//
// Based on research showing that total luminance reduction (not blue-light
// filtering alone) is the effective lever for circadian health. See:
//   - Phillips et al. (2019), PNAS — melatonin response to luminance
//   - Mineault (2026), "Blue light filters don't work"
//
// Key insight: melanopsin (the circadian photopigment) responds broadly to
// cyan/blue/green light. Halving blue via Night Shift ≈ 0.3 log-unit change —
// a tiny blip on vision's 6-order dynamic range. Reducing overall brightness
// by 90%+ achieves the 1–2 order-of-magnitude swing that actually matters.

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const appID = "org.pgsdf.displaycontrol"

// Profile presets based on circadian research.
type Profile struct {
	Name       string
	Brightness float64
	GammaR     float64
	GammaG     float64
	GammaB     float64
}

var (
	dayProfile = Profile{
		Name:       "Day",
		Brightness: 1.0,
		GammaR:     1.0,
		GammaG:     1.0,
		GammaB:     1.0,
	}

	// Night profile: aggressive overall dimming across ALL channels.
	// This achieves ~1 order of magnitude luminance reduction (10-15%
	// brightness), which is far more effective than blue-only filtering
	// (~0.3 log units). Gamma is reduced uniformly — the science shows
	// melanopsin responds broadly, so there is no reason to single out blue.
	nightProfile = Profile{
		Name:       "Night",
		Brightness: 0.35,
		GammaR:     0.85,
		GammaG:     0.85,
		GammaB:     0.85,
	}

	// Deep night for very dark environments / pre-sleep reading.
	deepNightProfile = Profile{
		Name:       "Deep Night",
		Brightness: 0.20,
		GammaR:     0.75,
		GammaG:     0.75,
		GammaB:     0.75,
	}
)

func main() {
	app := gtk.NewApplication(appID, 0)

	app.ConnectActivate(func() {
		if _, err := exec.LookPath("xrandr"); err != nil {
			showErrorDialog(nil, "Critical Dependency Missing",
				"This application requires `xrandr` to function.\nPlease install it and try again.")
			app.Quit()
			return
		}

		state := &State{
			brightness: 1.0,
			gammaR:     1.0,
			gammaG:     1.0,
			gammaB:     1.0,
		}

		var err error
		state.videoOutputs, err = detectVideoOutputs()
		if err != nil {
			showErrorDialog(nil, "Error Detecting Displays", err.Error())
			app.Quit()
			return
		}
		if len(state.videoOutputs) == 0 {
			showErrorDialog(nil, "No Displays Found",
				"Could not find any connected video outputs via xrandr.")
			app.Quit()
			return
		}
		state.activeOutput = state.videoOutputs[0]

		state.buildUI(app)
		state.updateControlsForActiveOutput()
		state.startScheduleChecker()
		state.window.Present()
	})

	if code := app.Run(os.Args); code > 0 {
		os.Exit(code)
	}
}

// State holds the application's state and widgets.
type State struct {
	window          *gtk.ApplicationWindow
	brightnessLabel *gtk.Label
	brightnessScale *gtk.Scale
	gammaRLabel     *gtk.Label
	gammaRScale     *gtk.Scale
	gammaGLabel     *gtk.Label
	gammaGScale     *gtk.Scale
	gammaBLabel     *gtk.Label
	gammaBScale     *gtk.Scale
	gammaLinkBtn    *gtk.ToggleButton
	outputDropDown  *gtk.DropDown
	profileLabel    *gtk.Label
	scheduleSwitch  *gtk.Switch
	dayHourSpin     *gtk.SpinButton
	dayMinSpin      *gtk.SpinButton
	nightHourSpin   *gtk.SpinButton
	nightMinSpin    *gtk.SpinButton

	// Internal state
	videoOutputs  []string
	activeOutput  string
	brightness    float64
	gammaR        float64
	gammaG        float64
	gammaB        float64
	gammaLinked   bool
	activeProfile string
	scheduleOn    bool
	suppressSync  bool // prevent feedback loops during programmatic updates
}

// buildUI constructs the main window and all widgets.
func (s *State) buildUI(app *gtk.Application) {
	s.window = gtk.NewApplicationWindow(app)
	s.window.SetTitle("Display Control")
	s.window.SetResizable(false)
	s.gammaLinked = true

	mainBox := gtk.NewBox(gtk.OrientationVertical, 0)
	mainBox.SetCSSClasses([]string{"main-container"})
	s.window.SetChild(mainBox)

	// ── Display Selector ─────────────────────────────────────────
	outputLabel := gtk.NewLabel("<b>Select Display</b>")
	outputLabel.SetUseMarkup(true)
	mainBox.Append(outputLabel)

	s.outputDropDown = gtk.NewDropDownFromStrings(s.videoOutputs)
	s.outputDropDown.SetSelected(0)
	s.outputDropDown.Connect("notify::selected", func() {
		s.activeOutput = s.videoOutputs[s.outputDropDown.Selected()]
		s.updateControlsForActiveOutput()
	})
	mainBox.Append(s.outputDropDown)
	mainBox.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	// ── Profile Buttons ──────────────────────────────────────────
	profileBox := gtk.NewBox(gtk.OrientationHorizontal, 8)
	profileBox.SetHAlign(gtk.AlignCenter)
	profileBox.SetMarginTop(4)
	profileBox.SetMarginBottom(4)

	s.profileLabel = gtk.NewLabel("")
	s.profileLabel.SetUseMarkup(true)

	btnDay := gtk.NewButton()
	btnDay.SetLabel("☀ Day")
	btnDay.SetTooltipText("Full brightness — bright daytime light is just as important\nfor circadian health as dim light at night.")
	btnDay.ConnectClicked(func() { s.applyProfile(dayProfile) })

	btnNight := gtk.NewButton()
	btnNight.SetLabel("🌙 Night")
	btnNight.SetTooltipText("Reduces total brightness significantly.\nThis is 5–10× more effective for sleep than\nblue-light-only filters (Phillips et al., 2019).")
	btnNight.ConnectClicked(func() { s.applyProfile(nightProfile) })

	btnDeepNight := gtk.NewButton()
	btnDeepNight.SetLabel("🌑 Deep Night")
	btnDeepNight.SetTooltipText("Aggressive dimming for pre-sleep reading.\nTargets ~1 order of magnitude luminance reduction.")
	btnDeepNight.ConnectClicked(func() { s.applyProfile(deepNightProfile) })

	profileBox.Append(btnDay)
	profileBox.Append(btnNight)
	profileBox.Append(btnDeepNight)
	mainBox.Append(profileBox)
	mainBox.Append(s.profileLabel)
	mainBox.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	// ── Brightness ───────────────────────────────────────────────
	s.brightnessLabel = gtk.NewLabel("")
	s.brightnessLabel.SetUseMarkup(true)
	mainBox.Append(s.brightnessLabel)

	brightnessBox := gtk.NewBox(gtk.OrientationHorizontal, 10)
	s.brightnessScale = gtk.NewScaleWithRange(gtk.OrientationHorizontal, 0.10, 1.0, 0.01)
	s.brightnessScale.SetHExpand(true)
	s.brightnessScale.ConnectValueChanged(func() {
		if s.suppressSync {
			return
		}
		s.brightness = s.brightnessScale.Value()
		s.updateBrightnessLabel()
		s.clearProfileLabel()
		s.applyXrandr()
	})
	btnDecBright := gtk.NewButtonFromIconName("go-previous-symbolic")
	btnDecBright.ConnectClicked(func() { s.stepScale(s.brightnessScale, -0.05, 0.10, 1.0) })
	btnIncBright := gtk.NewButtonFromIconName("go-next-symbolic")
	btnIncBright.ConnectClicked(func() { s.stepScale(s.brightnessScale, 0.05, 0.10, 1.0) })
	brightnessBox.Append(btnDecBright)
	brightnessBox.Append(s.brightnessScale)
	brightnessBox.Append(btnIncBright)
	mainBox.Append(brightnessBox)
	mainBox.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	// ── Per-Channel Gamma (R / G / B) ────────────────────────────
	gammaHeader := gtk.NewBox(gtk.OrientationHorizontal, 8)
	gammaTitle := gtk.NewLabel("<b>Gamma (per-channel)</b>")
	gammaTitle.SetUseMarkup(true)
	gammaTitle.SetHExpand(true)
	gammaTitle.SetHAlign(gtk.AlignStart)
	gammaTitle.SetTooltipText("Your circadian system responds to overall light intensity,\nnot just blue light. Uniform reduction is most effective.")

	s.gammaLinkBtn = gtk.NewToggleButton()
	s.gammaLinkBtn.SetLabel("🔗 Linked")
	s.gammaLinkBtn.SetActive(true)
	s.gammaLinkBtn.SetTooltipText("When linked, all three channels adjust together.\nThis is the recommended mode — melanopsin responds\nbroadly across cyan/blue/green wavelengths.")
	s.gammaLinkBtn.ConnectToggled(func() {
		s.gammaLinked = s.gammaLinkBtn.Active()
		if s.gammaLinked {
			s.gammaLinkBtn.SetLabel("🔗 Linked")
			// Sync G and B to R
			s.suppressSync = true
			s.gammaG = s.gammaR
			s.gammaB = s.gammaR
			s.gammaGScale.SetValue(s.gammaR)
			s.gammaBScale.SetValue(s.gammaR)
			s.suppressSync = false
			s.updateGammaLabels()
			s.applyXrandr()
		} else {
			s.gammaLinkBtn.SetLabel("🔓 Unlinked")
		}
	})
	gammaHeader.Append(gammaTitle)
	gammaHeader.Append(s.gammaLinkBtn)
	mainBox.Append(gammaHeader)

	// Red
	s.gammaRLabel = gtk.NewLabel("")
	s.gammaRLabel.SetUseMarkup(true)
	mainBox.Append(s.gammaRLabel)
	s.gammaRScale = s.makeGammaScale(func(v float64) {
		s.gammaR = v
		if s.gammaLinked {
			s.suppressSync = true
			s.gammaG = v
			s.gammaB = v
			s.gammaGScale.SetValue(v)
			s.gammaBScale.SetValue(v)
			s.suppressSync = false
		}
		s.updateGammaLabels()
		s.clearProfileLabel()
		s.applyXrandr()
	})
	mainBox.Append(s.makeGammaRow(s.gammaRScale))

	// Green
	s.gammaGLabel = gtk.NewLabel("")
	s.gammaGLabel.SetUseMarkup(true)
	mainBox.Append(s.gammaGLabel)
	s.gammaGScale = s.makeGammaScale(func(v float64) {
		s.gammaG = v
		s.updateGammaLabels()
		s.clearProfileLabel()
		s.applyXrandr()
	})
	mainBox.Append(s.makeGammaRow(s.gammaGScale))

	// Blue
	s.gammaBLabel = gtk.NewLabel("")
	s.gammaBLabel.SetUseMarkup(true)
	mainBox.Append(s.gammaBLabel)
	s.gammaBScale = s.makeGammaScale(func(v float64) {
		s.gammaB = v
		s.updateGammaLabels()
		s.clearProfileLabel()
		s.applyXrandr()
	})
	mainBox.Append(s.makeGammaRow(s.gammaBScale))

	mainBox.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	// ── Schedule Section ─────────────────────────────────────────
	schedHeader := gtk.NewBox(gtk.OrientationHorizontal, 8)
	schedTitle := gtk.NewLabel("<b>Auto Schedule</b>")
	schedTitle.SetUseMarkup(true)
	schedTitle.SetHExpand(true)
	schedTitle.SetHAlign(gtk.AlignStart)
	schedTitle.SetTooltipText("Automates the key recommendation: high luminance during the\nday, very low at night — creating the multi-order-of-magnitude\nswing that actually influences your circadian clock.")

	s.scheduleSwitch = gtk.NewSwitch()
	s.scheduleSwitch.SetActive(false)
	s.scheduleSwitch.Connect("notify::active", func() {
		s.scheduleOn = s.scheduleSwitch.Active()
		// Apply the correct profile immediately when enabled.
		s.checkScheduleNow()
	})

	schedHeader.Append(schedTitle)
	schedHeader.Append(s.scheduleSwitch)
	mainBox.Append(schedHeader)

	// Day time
	dayRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	dayRow.SetMarginStart(10)
	dayRowLabel := gtk.NewLabel("Day starts:")
	dayRowLabel.SetHExpand(true)
	dayRowLabel.SetHAlign(gtk.AlignStart)
	s.dayHourSpin = gtk.NewSpinButtonWithRange(0, 23, 1)
	s.dayHourSpin.SetValue(7)
	s.dayHourSpin.SetWidthChars(2)
	colonLabel1 := gtk.NewLabel(":")
	s.dayMinSpin = gtk.NewSpinButtonWithRange(0, 59, 15)
	s.dayMinSpin.SetValue(0)
	s.dayMinSpin.SetWidthChars(2)
	dayRow.Append(dayRowLabel)
	dayRow.Append(s.dayHourSpin)
	dayRow.Append(colonLabel1)
	dayRow.Append(s.dayMinSpin)
	mainBox.Append(dayRow)

	// Night time
	nightRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	nightRow.SetMarginStart(10)
	nightRowLabel := gtk.NewLabel("Night starts:")
	nightRowLabel.SetHExpand(true)
	nightRowLabel.SetHAlign(gtk.AlignStart)
	s.nightHourSpin = gtk.NewSpinButtonWithRange(0, 23, 1)
	s.nightHourSpin.SetValue(21)
	s.nightHourSpin.SetWidthChars(2)
	colonLabel2 := gtk.NewLabel(":")
	s.nightMinSpin = gtk.NewSpinButtonWithRange(0, 59, 15)
	s.nightMinSpin.SetValue(0)
	s.nightMinSpin.SetWidthChars(2)
	nightRow.Append(nightRowLabel)
	nightRow.Append(s.nightHourSpin)
	nightRow.Append(colonLabel2)
	nightRow.Append(s.nightMinSpin)
	mainBox.Append(nightRow)

	// ── Load CSS ─────────────────────────────────────────────────
	cssProvider := gtk.NewCSSProvider()
	css := `
		.main-container { padding: 15px; }
		label { font-size: 1.05em; }
		scale trough { min-height: 8px; }
		separator { margin-top: 6px; margin-bottom: 6px; }
		.profile-active { font-weight: bold; font-style: italic; }
	`
	cssProvider.LoadFromData(css)
	gtk.StyleContextAddProviderForDisplay(
		gdk.DisplayGetDefault(), cssProvider,
		gtk.STYLE_PROVIDER_PRIORITY_APPLICATION,
	)
}

// ── Helpers for gamma slider construction ─────────────────────────

func (s *State) makeGammaScale(onChange func(float64)) *gtk.Scale {
	scale := gtk.NewScaleWithRange(gtk.OrientationHorizontal, 0.5, 3.0, 0.01)
	scale.SetHExpand(true)
	scale.ConnectValueChanged(func() {
		if s.suppressSync {
			return
		}
		onChange(scale.Value())
	})
	return scale
}

func (s *State) makeGammaRow(scale *gtk.Scale) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	btnDec := gtk.NewButtonFromIconName("go-previous-symbolic")
	btnDec.ConnectClicked(func() { s.stepScale(scale, -0.1, 0.5, 3.0) })
	btnInc := gtk.NewButtonFromIconName("go-next-symbolic")
	btnInc.ConnectClicked(func() { s.stepScale(scale, 0.1, 0.5, 3.0) })
	box.Append(btnDec)
	box.Append(scale)
	box.Append(btnInc)
	return box
}

func (s *State) stepScale(scale *gtk.Scale, delta, min, max float64) {
	v := scale.Value() + delta
	if v < min {
		v = min
	}
	if v > max {
		v = max
	}
	scale.SetValue(v)
}

// ── Profile application ───────────────────────────────────────────

func (s *State) applyProfile(p Profile) {
	s.suppressSync = true
	s.brightness = p.Brightness
	s.gammaR = p.GammaR
	s.gammaG = p.GammaG
	s.gammaB = p.GammaB
	s.brightnessScale.SetValue(s.brightness)
	s.gammaRScale.SetValue(s.gammaR)
	s.gammaGScale.SetValue(s.gammaG)
	s.gammaBScale.SetValue(s.gammaB)
	s.suppressSync = false

	s.activeProfile = p.Name
	s.profileLabel.SetLabel(fmt.Sprintf("  <i>Active: %s</i>", p.Name))

	s.updateBrightnessLabel()
	s.updateGammaLabels()
	s.applyXrandr()
}

func (s *State) clearProfileLabel() {
	s.activeProfile = ""
	s.profileLabel.SetLabel("")
}

// ── Schedule checker ──────────────────────────────────────────────

// checkScheduleNow evaluates the current time against the day/night
// schedule and applies the appropriate profile if needed.
func (s *State) checkScheduleNow() {
	if !s.scheduleOn {
		return
	}

	now := time.Now()
	dayStart := int(s.dayHourSpin.Value())*60 + int(s.dayMinSpin.Value())
	nightStart := int(s.nightHourSpin.Value())*60 + int(s.nightMinSpin.Value())
	nowMin := now.Hour()*60 + now.Minute()

	isDaytime := false
	if dayStart < nightStart {
		isDaytime = nowMin >= dayStart && nowMin < nightStart
	} else {
		// Handles overnight wrap (e.g., night=22:00, day=06:00)
		isDaytime = nowMin >= dayStart || nowMin < nightStart
	}

	if isDaytime && s.activeProfile != dayProfile.Name {
		s.applyProfile(dayProfile)
	} else if !isDaytime && s.activeProfile != nightProfile.Name {
		s.applyProfile(nightProfile)
	}
}

func (s *State) startScheduleChecker() {
	// Poll every 10 seconds for responsive profile switching.
	glib.TimeoutSecondsAdd(10, func() bool {
		s.checkScheduleNow()
		return true // keep running
	})
}

// ── Label updaters ────────────────────────────────────────────────

func (s *State) updateBrightnessLabel() {
	s.brightnessLabel.SetLabel(fmt.Sprintf("<b>Brightness: %d%%</b>", int(s.brightness*100)))
}

func (s *State) updateGammaLabels() {
	s.gammaRLabel.SetLabel(fmt.Sprintf("  <span color='#cc3333'>R</span>  %.2f", s.gammaR))
	s.gammaGLabel.SetLabel(fmt.Sprintf("  <span color='#33aa33'>G</span>  %.2f", s.gammaG))
	s.gammaBLabel.SetLabel(fmt.Sprintf("  <span color='#3366cc'>B</span>  %.2f", s.gammaB))
}

// ── UI state sync ─────────────────────────────────────────────────

func (s *State) updateControlsForActiveOutput() {
	b, err := getCurrentBrightness(s.activeOutput)
	if err != nil {
		log.Printf("Warning: could not get brightness for %s: %v", s.activeOutput, err)
		s.brightness = 1.0
	} else {
		s.brightness = b
	}

	s.suppressSync = true
	s.brightnessScale.SetValue(s.brightness)
	s.gammaR = 1.0
	s.gammaG = 1.0
	s.gammaB = 1.0
	s.gammaRScale.SetValue(1.0)
	s.gammaGScale.SetValue(1.0)
	s.gammaBScale.SetValue(1.0)
	s.suppressSync = false

	s.updateBrightnessLabel()
	s.updateGammaLabels()
	s.clearProfileLabel()
	s.applyXrandr()
}

// ── xrandr interaction ────────────────────────────────────────────

func runXrandr(args ...string) (string, error) {
	cmd := exec.Command("xrandr", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("xrandr command failed: %v\nOutput: %s", err, string(output))
	}
	return string(output), nil
}

func detectVideoOutputs() ([]string, error) {
	out, err := runXrandr()
	if err != nil {
		return nil, err
	}
	var outputs []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " connected") && !strings.Contains(line, " disconnected") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				outputs = append(outputs, fields[0])
			}
		}
	}
	return outputs, nil
}

func getCurrentBrightness(outputName string) (float64, error) {
	out, err := runXrandr("--verbose", "--query")
	if err != nil {
		return 0, err
	}
	lines := strings.Split(out, "\n")
	inBlock := false
	for _, line := range lines {
		if strings.HasPrefix(line, outputName+" connected") {
			inBlock = true
		}
		if inBlock {
			if strings.Contains(line, "Brightness:") {
				fields := strings.Fields(line)
				if len(fields) == 2 {
					return strconv.ParseFloat(fields[1], 64)
				}
			}
			if strings.Contains(line, " connected") && !strings.HasPrefix(line, outputName) {
				break
			}
		}
	}
	return 0, fmt.Errorf("could not find brightness for %s", outputName)
}

func (s *State) applyXrandr() {
	bStr := strconv.FormatFloat(s.brightness, 'f', 2, 64)
	gStr := fmt.Sprintf("%.2f:%.2f:%.2f", s.gammaR, s.gammaG, s.gammaB)
	_, err := runXrandr("--output", s.activeOutput, "--brightness", bStr, "--gamma", gStr)
	if err != nil {
		log.Printf("Failed to set brightness/gamma: %v", err)
		showErrorDialog(&s.window.Window, "Failed to Apply Settings", err.Error())
	}
}

// showErrorDialog displays an error message.
func showErrorDialog(parent *gtk.Window, title, text string) {
	dialog := gtk.NewMessageDialog(parent, gtk.DialogModal, gtk.MessageError, gtk.ButtonsOK)
	dialog.SetMarkup(title)
	dialog.SetObjectProperty("secondary-text", text)
	dialog.ConnectResponse(func(int) { dialog.Destroy() })
	dialog.Show()
}
