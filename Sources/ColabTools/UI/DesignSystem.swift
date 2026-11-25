import AppKit
import ColabToolsCore
import SwiftUI

/// Unified Apple macOS HIG + CJK-friendly typography scale.
/// Eliminates sub-11pt Chinese text and aligns main desktop views with macOS 13pt body standards.
enum ColabTypography {
    static let pageTitle = Font.system(size: 22, weight: .semibold)
    static let sectionTitle = Font.system(size: 14, weight: .semibold)
    static let body = Font.system(size: 13.5, weight: .regular)
    static let bodyMedium = Font.system(size: 13.5, weight: .medium)
    static let bodySemibold = Font.system(size: 13.5, weight: .semibold)
    static let subheadline = Font.system(size: 12, weight: .regular)
    static let subheadlineMedium = Font.system(size: 12, weight: .medium)
    static let footnote = Font.system(size: 11.5, weight: .regular)
    static let footnoteMedium = Font.system(size: 11.5, weight: .medium)
    static let mono = Font.system(size: 12, design: .monospaced)
    static let monoSmall = Font.system(size: 11.5, design: .monospaced)

    // Menu bar popover scale (+1–1.5pt over previous cramped 10–11pt sizing)
    static let popoverTitle = Font.system(size: 14.5, weight: .semibold)
    static let popoverBody = Font.system(size: 13, weight: .regular)
    static let popoverBodyMedium = Font.system(size: 13, weight: .medium)
    static let popoverSub = Font.system(size: 12, weight: .regular)
    static let popoverSubMedium = Font.system(size: 12, weight: .medium)
    static let popoverCaption = Font.system(size: 11, weight: .regular)
    static let popoverCaptionMedium = Font.system(size: 11, weight: .medium)
}

enum ColabTheme {
    static let amber = Color(red: 0.96, green: 0.65, blue: 0.14)
    static let emerald = Color(red: 0.22, green: 0.80, blue: 0.56)
    static let sky = Color(red: 0.36, green: 0.60, blue: 0.96)
    static let cyan = Color(red: 0.20, green: 0.78, blue: 0.86)
    static let coral = Color(red: 0.94, green: 0.36, blue: 0.36)
    static let indigo = Color(red: 0.54, green: 0.56, blue: 0.96)
    static let rose = Color(red: 0.94, green: 0.42, blue: 0.56)

    static func color(for kind: WorkloadKind) -> Color {
        switch kind {
        case .llm: return sky
        case .video: return indigo
        case .image: return rose
        case .audio: return cyan
        case .music: return amber
        case .general: return sky
        }
    }

    static func color(for tag: AccountColorTag) -> Color {
        switch tag {
        case .amber: return amber
        case .slate: return Color(red: 0.52, green: 0.58, blue: 0.66)
        case .emerald: return emerald
        case .indigo: return Color(red: 0.46, green: 0.49, blue: 0.96)
        case .rose: return Color(red: 0.94, green: 0.34, blue: 0.46)
        case .cyan: return cyan
        }
    }

    static func statusColor(for phase: InstancePhase) -> Color {
        switch phase {
        case .running: return emerald
        case .starting, .stopping, .queuing: return amber
        case .error: return coral
        case .offline: return Color(nsColor: .tertiaryLabelColor)
        }
    }
}

enum Formatters {
    static func duration(_ seconds: Int) -> String {
        let s = max(0, seconds)
        let d = s / 86400
        let h = (s % 86400) / 3600
        let m = (s % 3600) / 60
        if d > 0 { return "\(d)天 \(h)小时" }
        if h > 0 { return "\(h)h \(String(format: "%02d", m))m" }
        return "\(m)m \(String(format: "%02d", s % 60))s"
    }

    static func shortMinutes(_ seconds: Int) -> String {
        let m = max(0, (seconds + 59) / 60)
        if m >= 60 { return "\(m / 60)h \(m % 60)m" }
        return "\(m)m"
    }

    static func tokens(_ count: Int) -> String {
        if count >= 1000 { return String(format: "%.1fK", Double(count) / 1000.0) }
        return "\(count)"
    }

    static func cu(_ value: Double) -> String {
        if value >= 1000 { return String(format: "%.1f", value) }
        if value >= 100 { return String(format: "%.1f", value) }
        return String(format: "%.2f", value)
    }

    static func cuCompact(_ value: Double) -> String {
        if value >= 1000 { return String(format: "%.2fK", value / 1000.0) }
        return String(format: "%.0f", value)
    }
}

// MARK: - Refined macOS Surfaces

extension View {
    /// Tactile macOS HUD / Bento card surface with continuous corners and subtle hairline rim.
    func hudCard(cornerRadius: CGFloat = 14) -> some View {
        self
            .background(Color.primary.opacity(0.052), in: RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                    .strokeBorder(Color.primary.opacity(0.095), lineWidth: 0.65)
            )
    }

    func bentoCard(cornerRadius: CGFloat = 16) -> some View {
        self
            .background(Color.primary.opacity(0.052), in: RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                    .strokeBorder(Color.primary.opacity(0.095), lineWidth: 0.7)
            )
    }

    func hudPill() -> some View {
        self
            .font(ColabTypography.popoverCaptionMedium)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 8.5)
            .padding(.vertical, 4)
            .background(Color.primary.opacity(0.068), in: RoundedRectangle(cornerRadius: 6.5, style: .continuous))
    }
}

// MARK: - Shared App Icon Loader

struct ColabAppIconView: View {
    let size: CGFloat
    var cornerRadius: CGFloat? = nil

    var body: some View {
        let radius = cornerRadius ?? (size * 0.24)
        let iconImage: NSImage? = {
            if let iconPath = Bundle.main.path(forResource: "AppIconPreview", ofType: "png"),
               let img = NSImage(contentsOfFile: iconPath) {
                return img
            }
            let devAsset = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
                .appendingPathComponent("assets/AppIcon.png")
            if let img = NSImage(contentsOfFile: devAsset.path) {
                return img
            }
            return nil
        }()

        if let img = iconImage {
            Image(nsImage: img)
                .resizable()
                .scaledToFit()
                .frame(width: size, height: size)
                .clipShape(RoundedRectangle(cornerRadius: radius, style: .continuous))
        } else {
            ZStack {
                LinearGradient(
                    colors: [ColabTheme.amber, Color(red: 0.92, green: 0.44, blue: 0.12)],
                    startPoint: .topLeading,
                    endPoint: .bottomTrailing
                )
                Image(systemName: "cpu.fill")
                    .font(.system(size: size * 0.48, weight: .semibold))
                    .foregroundStyle(.white)
            }
            .frame(width: size, height: size)
            .clipShape(RoundedRectangle(cornerRadius: radius, style: .continuous))
        }
    }
}

// MARK: - Hero Numeric Display (Large value + baseline-aligned subtle unit)

struct BigMetricValue: View {
    let number: String
    let unit: String
    var fontSize: CGFloat = 26

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 4) {
            Text(number)
                .font(.system(size: fontSize, weight: .semibold, design: .rounded))
                .monospacedDigit()
                .foregroundStyle(.primary)
                .lineLimit(1)
                .minimumScaleFactor(0.75)
            if !unit.isEmpty {
                Text(unit)
                    .font(.system(size: max(12, fontSize * 0.48), weight: .medium))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
    }
}

// MARK: - Top-Right Soft Status Capsule inside Cards

struct CardBadge: View {
    let text: String
    var tint: Color? = nil

    var body: some View {
        Text(text)
            .font(ColabTypography.footnoteMedium)
            .monospacedDigit()
            .foregroundStyle(tint ?? .secondary)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background((tint ?? Color.primary).opacity(tint == nil ? 0.068 : 0.14), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .lineLimit(1)
            .fixedSize(horizontal: true, vertical: false)
    }
}

// MARK: - Micro Visualizations (Mole-Inspired Warm Clarity)

/// Glowing horizon line progress bar (inspired by Mole memory card).
struct HorizonBar: View {
    let progress: Double
    let tint: Color
    var height: CGFloat = 7

    var body: some View {
        GeometryReader { geo in
            let clamped = min(1.0, max(0.0, progress))
            let w = max(clamped > 0 ? 8 : 0, geo.size.width * clamped)
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(Color.primary.opacity(0.085))
                    .frame(height: height)
                Capsule()
                    .fill(
                        LinearGradient(
                            colors: [tint.opacity(0.76), tint],
                            startPoint: .leading,
                            endPoint: .trailing
                        )
                    )
                    .frame(width: w, height: height)
                    .shadow(color: tint.opacity(0.32), radius: 5, y: 1)
            }
        }
        .frame(height: height + 2)
    }
}

/// 10-segment micro equalizer bar (clean tactile readout for GPU load / VRAM pressure).
struct MicroSegmentsBar: View {
    let progress: Double
    let tint: Color
    var segments: Int = 10
    var maxHeight: CGFloat = 20

    var body: some View {
        HStack(alignment: .bottom, spacing: 4) {
            ForEach(0..<segments, id: \.self) { idx in
                let threshold = Double(idx + 1) / Double(segments)
                let filled = progress >= threshold - 0.05
                let heightRatio = 0.42 + 0.58 * (Double(idx + 1) / Double(segments))
                RoundedRectangle(cornerRadius: 2.5, style: .continuous)
                    .fill(filled ? tint.opacity(0.92) : Color.primary.opacity(0.09))
                    .frame(maxWidth: .infinity)
                    .frame(height: maxHeight * (filled ? heightRatio : 0.32))
            }
        }
        .frame(height: maxHeight + 2, alignment: .bottom)
    }
}

/// Smooth subtle sparkline chart for real-time GPU / activity trends.
struct MicroSparkline: View {
    let points: [Double]
    let tint: Color
    var chartHeight: CGFloat = 24

    var body: some View {
        GeometryReader { geo in
            let w = geo.size.width
            let h = geo.size.height
            let data = points.count >= 2 ? points : [4, 4, 4, 4, 4, 4]
            let maxVal = max(100.0, data.max() ?? 100.0)
            let stepX = w / CGFloat(max(1, data.count - 1))

            let path = Path { p in
                for (i, v) in data.enumerated() {
                    let x = CGFloat(i) * stepX
                    let y = h - (CGFloat(v / maxVal) * (h - 4)) - 2
                    if i == 0 { p.move(to: CGPoint(x: x, y: y)) }
                    else { p.addLine(to: CGPoint(x: x, y: y)) }
                }
            }

            let fillPath = Path { p in
                p.addPath(path)
                p.addLine(to: CGPoint(x: w, y: h))
                p.addLine(to: CGPoint(x: 0, y: h))
                p.closeSubpath()
            }

            ZStack {
                fillPath.fill(
                    LinearGradient(
                        colors: [tint.opacity(0.24), tint.opacity(0.0)],
                        startPoint: .top,
                        endPoint: .bottom
                    )
                )
                path.stroke(tint.opacity(0.92), style: StrokeStyle(lineWidth: 1.8, lineCap: .round, lineJoin: .round))
            }
        }
        .frame(height: chartHeight)
    }
}

/// Compact circular progress ring (inspired by Mole battery card).
struct RingGauge: View {
    let progress: Double
    let tint: Color
    let icon: String
    var size: CGFloat = 44

    var body: some View {
        let clamped = min(1.0, max(0.0, progress))
        ZStack {
            Circle()
                .stroke(Color.primary.opacity(0.10), lineWidth: 4.5)
            Circle()
                .trim(from: 0, to: clamped)
                .stroke(tint, style: StrokeStyle(lineWidth: 4.5, lineCap: .round))
                .rotationEffect(.degrees(-90))
            Image(systemName: icon)
                .font(.system(size: size * 0.34, weight: .semibold))
                .foregroundStyle(tint)
        }
        .frame(width: size, height: size)
    }
}

/// Restrained warm status orb for the hero health/CU Bento card.
struct StatusAuraOrb: View {
    let phase: InstancePhase
    var size: CGFloat = 48

    private var orbColors: [Color] {
        switch phase {
        case .running:
            return [ColabTheme.emerald.opacity(0.95), ColabTheme.cyan.opacity(0.75)]
        case .queuing, .starting, .stopping:
            return [ColabTheme.amber, Color(red: 0.92, green: 0.45, blue: 0.14)]
        case .error:
            return [ColabTheme.coral, Color(red: 0.80, green: 0.22, blue: 0.28)]
        case .offline:
            return [Color.primary.opacity(0.24), Color.primary.opacity(0.10)]
        }
    }

    private var symbolName: String {
        switch phase {
        case .running: return "bolt.fill"
        case .queuing: return "clock.arrow.circlepath"
        case .starting, .stopping: return "arrow.triangle.2.circlepath"
        case .error: return "exclamationmark"
        case .offline: return "moon.fill"
        }
    }

    var body: some View {
        ZStack {
            Circle()
                .fill(
                    RadialGradient(
                        colors: [orbColors[0].opacity(0.32), orbColors[1].opacity(0.0)],
                        center: .center,
                        startRadius: size * 0.15,
                        endRadius: size * 0.65
                    )
                )
                .frame(width: size * 1.25, height: size * 1.25)

            Circle()
                .fill(
                    LinearGradient(
                        colors: orbColors,
                        startPoint: .topLeading,
                        endPoint: .bottomTrailing
                    )
                )
                .frame(width: size, height: size)
                .overlay(
                    Circle().strokeBorder(.white.opacity(0.22), lineWidth: 0.8)
                )
                .shadow(color: orbColors[0].opacity(0.28), radius: 8, y: 2)

            Image(systemName: symbolName)
                .font(.system(size: size * 0.40, weight: .semibold))
                .foregroundStyle(phase == .offline ? Color.primary.opacity(0.65) : .white)
        }
        .frame(width: size, height: size)
    }
}

/// Mole-inspired floating centered pill navigation bar for the main desktop window.
struct FloatingCapsuleNavBar: View {
    @Binding var selection: WorkspaceStore.MainSection
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        HStack(spacing: 4) {
            ColabAppIconView(size: 28, cornerRadius: 14)
                .padding(.leading, 4)
                .padding(.trailing, 4)

            ForEach(WorkspaceStore.MainSection.allCases) { sec in
                let isSelected = selection == sec
                Button {
                    withAnimation(.snappy(duration: 0.18)) {
                        selection = sec
                    }
                } label: {
                    Text(sec.rawValue)
                        .font(.system(size: 13.5, weight: isSelected ? .semibold : .medium))
                        .foregroundStyle(
                            isSelected
                                ? (colorScheme == .dark ? Color(red: 0.12, green: 0.11, blue: 0.10) : Color.white)
                                : Color.primary.opacity(0.76)
                        )
                        .padding(.horizontal, 16)
                        .padding(.vertical, 7)
                        .background {
                            if isSelected {
                                Capsule()
                                    .fill(colorScheme == .dark ? Color.white.opacity(0.95) : Color(red: 0.16, green: 0.17, blue: 0.20))
                                    .shadow(color: .black.opacity(0.14), radius: 4, y: 1)
                            }
                        }
                        .contentShape(Capsule())
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, 5)
        .padding(.vertical, 5)
        .background(
            Capsule()
                .fill(colorScheme == .dark ? Color.white.opacity(0.08) : Color.black.opacity(0.06))
        )
        .overlay(
            Capsule()
                .strokeBorder(Color.primary.opacity(0.10), lineWidth: 0.7)
        )
    }
}

// MARK: - Standardized Telemetry Card Header

struct TileHeader: View {
    let icon: String
    let iconTint: Color
    let title: String
    let badgeText: String
    var badgeTint: Color? = nil

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: icon)
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(iconTint)
            Text(title)
                .font(ColabTypography.popoverSubMedium)
                .foregroundStyle(.secondary)
            Spacer()
            if !badgeText.isEmpty {
                CardBadge(text: badgeText, tint: badgeTint)
            }
        }
    }
}

struct QuickActionButton: View {
    let title: String
    let icon: String
    var disabled: Bool = false
    var active: Bool = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Image(systemName: icon)
                    .font(.system(size: 12, weight: .medium))
                Text(title)
                    .font(.system(size: 12, weight: .medium))
                    .lineLimit(1)
                    .fixedSize(horizontal: true, vertical: false)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 8)
            .padding(.horizontal, 6)
            .background(
                active ? ColabTheme.amber.opacity(0.17) : Color.primary.opacity(disabled ? 0.04 : 0.065),
                in: RoundedRectangle(cornerRadius: 8.5, style: .continuous)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 8.5, style: .continuous)
                    .strokeBorder(active ? ColabTheme.amber.opacity(0.45) : Color.primary.opacity(disabled ? 0.08 : 0.11), lineWidth: 0.6)
            )
            .foregroundStyle(disabled ? Color.secondary.opacity(0.75) : Color.primary)
        }
        .buttonStyle(.plain)
        .disabled(disabled)
    }
}
