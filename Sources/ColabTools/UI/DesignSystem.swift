import ColabToolsCore
import SwiftUI

enum ColabTheme {
    static let amber = Color(red: 0.96, green: 0.65, blue: 0.14)
    static let emerald = Color(red: 0.22, green: 0.80, blue: 0.56)
    static let sky = Color(red: 0.36, green: 0.60, blue: 0.96)
    static let cyan = Color(red: 0.20, green: 0.78, blue: 0.86)
    static let coral = Color(red: 0.94, green: 0.36, blue: 0.36)

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
    /// Warm, tactile macOS HUD card surface with 14pt continuous corners and crisp hairline rim.
    func hudCard(cornerRadius: CGFloat = 14) -> some View {
        self
            .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                    .strokeBorder(Color.primary.opacity(0.095), lineWidth: 0.6)
            )
    }

    func hudPill() -> some View {
        self
            .font(.system(size: 11, weight: .medium))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 8)
            .padding(.vertical, 3.5)
            .background(Color.primary.opacity(0.065), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
    }
}

// MARK: - Hero Numeric Display (Large value + baseline-aligned subtle unit)

struct BigMetricValue: View {
    let number: String
    let unit: String
    var fontSize: CGFloat = 24

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 3) {
            Text(number)
                .font(.system(size: fontSize, weight: .semibold, design: .rounded))
                .monospacedDigit()
                .foregroundStyle(.primary)
                .lineLimit(1)
                .minimumScaleFactor(0.75)
            if !unit.isEmpty {
                Text(unit)
                    .font(.system(size: max(11, fontSize * 0.5), weight: .medium))
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
            .font(.system(size: 10.5, weight: .medium))
            .monospacedDigit()
            .foregroundStyle(tint ?? .secondary)
            .padding(.horizontal, 7)
            .padding(.vertical, 2.5)
            .background((tint ?? Color.primary).opacity(tint == nil ? 0.065 : 0.14), in: RoundedRectangle(cornerRadius: 5.5, style: .continuous))
            .lineLimit(1)
    }
}

// MARK: - Micro Visualizations (Refined & Subtle, Zero Visual Clutter)

/// Glowing horizon line progress bar (inspired by refined system memory cards).
struct HorizonBar: View {
    let progress: Double
    let tint: Color

    var body: some View {
        GeometryReader { geo in
            let clamped = min(1.0, max(0.0, progress))
            let w = max(clamped > 0 ? 6 : 0, geo.size.width * clamped)
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(Color.primary.opacity(0.08))
                    .frame(height: 6)
                Capsule()
                    .fill(
                        LinearGradient(
                            colors: [tint.opacity(0.78), tint],
                            startPoint: .leading,
                            endPoint: .trailing
                        )
                    )
                    .frame(width: w, height: 6)
                    .shadow(color: tint.opacity(0.32), radius: 4, y: 1)
            }
        }
        .frame(height: 8)
    }
}

/// 10-segment micro equalizer bar (clean tactile readout for GPU load / VRAM pressure).
struct MicroSegmentsBar: View {
    let progress: Double
    let tint: Color
    var segments: Int = 10

    var body: some View {
        HStack(alignment: .bottom, spacing: 3.5) {
            ForEach(0..<segments, id: \.self) { idx in
                let threshold = Double(idx + 1) / Double(segments)
                let filled = progress >= threshold - 0.05
                let heightRatio = 0.42 + 0.58 * (Double(idx + 1) / Double(segments))
                RoundedRectangle(cornerRadius: 2, style: .continuous)
                    .fill(filled ? tint.opacity(0.9) : Color.primary.opacity(0.09))
                    .frame(maxWidth: .infinity)
                    .frame(height: 16 * (filled ? heightRatio : 0.32))
            }
        }
        .frame(height: 18, alignment: .bottom)
    }
}

/// Smooth subtle sparkline chart for real-time GPU / activity trends.
struct MicroSparkline: View {
    let points: [Double]
    let tint: Color

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
                        colors: [tint.opacity(0.22), tint.opacity(0.0)],
                        startPoint: .top,
                        endPoint: .bottom
                    )
                )
                path.stroke(tint.opacity(0.92), style: StrokeStyle(lineWidth: 1.6, lineCap: .round, lineJoin: .round))
            }
        }
        .frame(height: 20)
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
        HStack(spacing: 5) {
            Image(systemName: icon)
                .font(.system(size: 11.5, weight: .medium))
                .foregroundStyle(iconTint)
            Text(title)
                .font(.system(size: 12, weight: .medium))
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
            HStack(spacing: 4) {
                Image(systemName: icon)
                    .font(.system(size: 10.5, weight: .medium))
                Text(title)
                    .font(.system(size: 11, weight: .medium))
                    .lineLimit(1)
                    .fixedSize(horizontal: true, vertical: false)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 7)
            .padding(.horizontal, 4)
            .background(
                active ? ColabTheme.amber.opacity(0.16) : Color.primary.opacity(disabled ? 0.025 : 0.06),
                in: RoundedRectangle(cornerRadius: 8, style: .continuous)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .strokeBorder(active ? ColabTheme.amber.opacity(0.45) : Color.primary.opacity(disabled ? 0.04 : 0.1), lineWidth: 0.5)
            )
            .foregroundStyle(disabled ? .tertiary : .primary)
        }
        .buttonStyle(.plain)
        .disabled(disabled)
    }
}
