import ColabToolsCore
import SwiftUI

enum ColabTheme {
    static let amber = Color(red: 0.96, green: 0.62, blue: 0.04)

    static func color(for tag: AccountColorTag) -> Color {
        switch tag {
        case .amber: return Color(red: 0.96, green: 0.62, blue: 0.04)
        case .slate: return Color(red: 0.44, green: 0.50, blue: 0.58)
        case .emerald: return Color(red: 0.06, green: 0.72, blue: 0.50)
        case .indigo: return Color(red: 0.39, green: 0.40, blue: 0.95)
        case .rose: return Color(red: 0.95, green: 0.25, blue: 0.37)
        case .cyan: return Color(red: 0.02, green: 0.71, blue: 0.83)
        }
    }

    static func statusColor(for phase: InstancePhase) -> Color {
        switch phase {
        case .running: return Color(nsColor: .systemGreen)
        case .starting, .stopping: return amber
        case .error: return Color(nsColor: .systemRed)
        case .offline: return Color(nsColor: .tertiaryLabelColor)
        }
    }
}

enum Formatters {
    static func duration(_ seconds: Int) -> String {
        let s = max(0, seconds)
        let h = s / 3600
        let m = (s % 3600) / 60
        if h > 0 { return "\(h)h \(String(format: "%02d", m))m" }
        return "\(m)m \(String(format: "%02d", s % 60))s"
    }

    static func shortMinutes(_ seconds: Int) -> String {
        let m = max(0, (seconds + 59) / 60)
        if m >= 60 { return "\(m / 60)h\(m % 60)m" }
        return "\(m)m"
    }

    static func tokens(_ count: Int) -> String {
        if count >= 1000 { return String(format: "%.1fK", Double(count) / 1000.0) }
        return "\(count)"
    }

    static func cu(_ value: Double) -> String {
        if value >= 1000 { return String(format: "%.0f", value) }
        if value >= 100 { return String(format: "%.1f", value) }
        return String(format: "%.2f", value)
    }
}

extension View {
    func refinedCard() -> some View {
        self
            .background(Color.primary.opacity(0.032), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .strokeBorder(Color.primary.opacity(0.085), lineWidth: 0.5)
            )
    }
}

struct MetricTile: View {
    let title: String
    let value: String
    let subtitle: String
    let progress: Double?
    var accentHigh: Bool = false

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(title)
                .font(.system(size: 10.5))
                .foregroundStyle(.secondary)
            Text(value)
                .font(.system(size: 14, weight: .semibold))
                .monospacedDigit()
                .lineLimit(1)
                .minimumScaleFactor(0.82)
            if let progress {
                GeometryReader { geo in
                    ZStack(alignment: .leading) {
                        Capsule().fill(Color.primary.opacity(0.08))
                        Capsule()
                            .fill((accentHigh && progress > 0.9) ? ColabTheme.amber : Color.primary.opacity(0.55))
                            .frame(width: max(0, geo.size.width * min(1.0, progress)))
                    }
                }
                .frame(height: 3.5)
            }
            Text(subtitle)
                .font(.system(size: 10))
                .foregroundStyle(.tertiary)
                .lineLimit(1)
                .minimumScaleFactor(0.82)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(10)
        .refinedCard()
    }
}

struct ActionChip: View {
    let title: String
    let icon: String
    var disabled: Bool = false
    var active: Bool = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Image(systemName: icon)
                    .font(.system(size: 10.5))
                Text(title)
                    .font(.system(size: 10.5, weight: .medium))
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 6)
            .padding(.horizontal, 6)
            .background(
                active ? ColabTheme.amber.opacity(0.14) : Color.primary.opacity(disabled ? 0.02 : 0.055),
                in: RoundedRectangle(cornerRadius: 7, style: .continuous)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 7, style: .continuous)
                    .strokeBorder(active ? ColabTheme.amber.opacity(0.4) : Color.primary.opacity(disabled ? 0.04 : 0.11), lineWidth: 0.5)
            )
            .foregroundStyle(disabled ? .tertiary : .primary)
        }
        .buttonStyle(.plain)
        .disabled(disabled)
    }
}

struct KeyValueRow: View {
    let label: String
    let value: String

    var body: some View {
        HStack(spacing: 6) {
            Text(label)
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .frame(width: 68, alignment: .leading)
            Text(value)
                .font(.system(size: 11, design: .monospaced))
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer()
        }
    }
}
