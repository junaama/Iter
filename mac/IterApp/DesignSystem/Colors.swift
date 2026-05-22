import SwiftUI

extension Color {
    static func iterBackground(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).background }
    static func iterPanel(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).panel }
    static func iterSidebar(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).sidebar }
    static func iterRail(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).rail }
    static func iterBorder(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).border }
    static func iterBorderStrong(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).borderStrong }
    static func iterHover(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).hover }
    static func iterSelected(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).selected }
    static func iterTextPrimary(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).textPrimary }
    static func iterTextSecondary(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).textSecondary }
    static func iterTextTertiary(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).textTertiary }
    static func iterTextQuaternary(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).textQuaternary }
    static func iterAccent(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).accent }
    static func iterAccentSoft(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).accentSoft }
    static func iterGood(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).good }
    static func iterGoodSoft(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).goodSoft }
    static func iterWarn(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).warn }
    static func iterWarnSoft(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).warnSoft }
    static func iterBad(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).bad }
    static func iterBadSoft(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).badSoft }
    static func iterStageBackdrop(for scheme: ColorScheme) -> Color { IterPalette.current(scheme).stageBackdrop }
}

struct IterPalette {
    let background: Color
    let panel: Color
    let sidebar: Color
    let rail: Color
    let border: Color
    let borderStrong: Color
    let hover: Color
    let selected: Color
    let textPrimary: Color
    let textSecondary: Color
    let textTertiary: Color
    let textQuaternary: Color
    let accent: Color
    let accentSoft: Color
    let good: Color
    let goodSoft: Color
    let warn: Color
    let warnSoft: Color
    let bad: Color
    let badSoft: Color
    let stageBackdrop: Color

    static func current(_ scheme: ColorScheme) -> IterPalette {
        switch scheme {
        case .dark:
            return .dark
        default:
            return .light
        }
    }

    static let light = IterPalette(
        background: .hex(0xFBFAF7),
        panel: .hex(0xFFFFFF),
        sidebar: .hex(0xF4F2ED),
        rail: .hex(0xF9F7F2),
        border: .rgba(20, 18, 14, 0.08),
        borderStrong: .rgba(20, 18, 14, 0.14),
        hover: .rgba(20, 18, 14, 0.04),
        selected: .rgba(20, 18, 14, 0.06),
        textPrimary: .hex(0x14120E),
        textSecondary: .hex(0x5C5851),
        textTertiary: .hex(0x97928A),
        textQuaternary: .hex(0xC7C2BA),
        accent: .oklch(lightness: 0.66, chroma: 0.15, hue: 38),
        accentSoft: .oklch(lightness: 0.66, chroma: 0.15, hue: 38, alpha: 0.10),
        good: .oklch(lightness: 0.58, chroma: 0.13, hue: 145),
        goodSoft: .oklch(lightness: 0.58, chroma: 0.13, hue: 145, alpha: 0.12),
        warn: .oklch(lightness: 0.70, chroma: 0.13, hue: 75),
        warnSoft: .oklch(lightness: 0.70, chroma: 0.13, hue: 75, alpha: 0.14),
        bad: .oklch(lightness: 0.58, chroma: 0.16, hue: 25),
        badSoft: .oklch(lightness: 0.58, chroma: 0.16, hue: 25, alpha: 0.12),
        stageBackdrop: .hex(0xDCD8D1)
    )

    static let dark = IterPalette(
        background: .hex(0x0D0D0F),
        panel: .hex(0x131316),
        sidebar: .hex(0x0A0A0C),
        rail: .hex(0x101013),
        border: .rgba(255, 255, 255, 0.08),
        borderStrong: .rgba(255, 255, 255, 0.14),
        hover: .rgba(255, 255, 255, 0.04),
        selected: .rgba(255, 255, 255, 0.06),
        textPrimary: .hex(0xE9E7E2),
        textSecondary: .hex(0x9B978F),
        textTertiary: .hex(0x67625B),
        textQuaternary: .hex(0x3E3A35),
        accent: .oklch(lightness: 0.72, chroma: 0.15, hue: 38),
        accentSoft: .oklch(lightness: 0.72, chroma: 0.15, hue: 38, alpha: 0.14),
        good: .oklch(lightness: 0.72, chroma: 0.14, hue: 145),
        goodSoft: .oklch(lightness: 0.72, chroma: 0.14, hue: 145, alpha: 0.14),
        warn: .oklch(lightness: 0.78, chroma: 0.13, hue: 75),
        warnSoft: .oklch(lightness: 0.78, chroma: 0.13, hue: 75, alpha: 0.16),
        bad: .oklch(lightness: 0.68, chroma: 0.16, hue: 25),
        badSoft: .oklch(lightness: 0.68, chroma: 0.16, hue: 25, alpha: 0.14),
        stageBackdrop: .hex(0x050506)
    )
}

enum IterHarnessTint: String, CaseIterable {
    case claudeCode = "claude-code"
    case codex
    case opencode
    case gemini
    case piHarness = "pi"

    var shortCode: String {
        switch self {
        case .claudeCode: return "cc"
        case .codex: return "cx"
        case .opencode: return "oc"
        case .gemini: return "gm"
        case .piHarness: return "pi"
        }
    }

    var color: Color {
        switch self {
        case .claudeCode: return .oklch(lightness: 0.68, chroma: 0.14, hue: 38)
        case .codex: return .oklch(lightness: 0.65, chroma: 0.11, hue: 250)
        case .opencode: return .oklch(lightness: 0.62, chroma: 0.12, hue: 145)
        case .gemini: return .oklch(lightness: 0.68, chroma: 0.10, hue: 295)
        case .piHarness: return .oklch(lightness: 0.70, chroma: 0.11, hue: 75)
        }
    }
}

enum IterAvatarTint: String, CaseIterable {
    case priya
    case mchen
    case ana
    case yusuf
    case lena
    case jin
    case sara
    case tom

    var color: Color {
        switch self {
        case .priya: return .oklch(lightness: 0.55, chroma: 0.14, hue: 38)
        case .mchen: return .oklch(lightness: 0.55, chroma: 0.12, hue: 250)
        case .ana: return .oklch(lightness: 0.55, chroma: 0.13, hue: 145)
        case .yusuf: return .oklch(lightness: 0.55, chroma: 0.13, hue: 295)
        case .lena: return .oklch(lightness: 0.55, chroma: 0.13, hue: 75)
        case .jin: return .oklch(lightness: 0.55, chroma: 0.12, hue: 200)
        case .sara: return .oklch(lightness: 0.55, chroma: 0.13, hue: 5)
        case .tom: return .oklch(lightness: 0.55, chroma: 0.10, hue: 100)
        }
    }
}

extension Color {
    static func hex(_ value: UInt32, alpha: Double = 1) -> Color {
        let red = Double((value >> 16) & 0xFF) / 255
        let green = Double((value >> 8) & 0xFF) / 255
        let blue = Double(value & 0xFF) / 255
        return Color(.sRGB, red: red, green: green, blue: blue, opacity: alpha)
    }

    static func rgba(_ red: Double, _ green: Double, _ blue: Double, _ alpha: Double) -> Color {
        Color(.sRGB, red: red / 255, green: green / 255, blue: blue / 255, opacity: alpha)
    }

    static func oklch(lightness: Double, chroma: Double, hue: Double, alpha: Double = 1) -> Color {
        let hueRadians = hue * .pi / 180
        let oklabA = chroma * cos(hueRadians)
        let oklabB = chroma * sin(hueRadians)

        let long = lightness + 0.3963377774 * oklabA + 0.2158037573 * oklabB
        let medium = lightness - 0.1055613458 * oklabA - 0.0638541728 * oklabB
        let short = lightness - 0.0894841775 * oklabA - 1.2914855480 * oklabB

        let longCubed = long * long * long
        let mediumCubed = medium * medium * medium
        let shortCubed = short * short * short

        let linearRed = 4.0767416621 * longCubed - 3.3077115913 * mediumCubed + 0.2309699292 * shortCubed
        let linearGreen = -1.2684380046 * longCubed + 2.6097574011 * mediumCubed - 0.3413193965 * shortCubed
        let linearBlue = -0.0041960863 * longCubed - 0.7034186147 * mediumCubed + 1.7076147010 * shortCubed

        return Color(
            .sRGB,
            red: gammaCorrect(linearRed),
            green: gammaCorrect(linearGreen),
            blue: gammaCorrect(linearBlue),
            opacity: alpha
        )
    }

    private static func gammaCorrect(_ value: Double) -> Double {
        let clamped = min(max(value, 0), 1)
        if clamped <= 0.0031308 {
            return 12.92 * clamped
        }
        return 1.055 * pow(clamped, 1 / 2.4) - 0.055
    }
}
