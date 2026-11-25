import AppKit

/// Draws a crisp, custom macOS Template menu bar icon inspired by Google Colab's interlocking loops
/// with subtle state cues (hollow outline when idle, bolder loops + center spark when running/active).
enum StatusBarIcon {
    static func makeTemplateImage(isRunning: Bool, isProcessing: Bool) -> NSImage {
        let size = NSSize(width: 20, height: 18)
        let image = NSImage(size: size, flipped: false) { rect in
            NSColor.black.setStroke()
            NSColor.black.setFill()

            let lineWidth: CGFloat = isRunning ? 2.1 : 1.55
            let r: CGFloat = 4.5
            let cy = rect.midY
            let leftCenter = NSPoint(x: rect.midX - 3.6, y: cy)
            let rightCenter = NSPoint(x: rect.midX + 3.6, y: cy)

            // Left Colab loop (C-arc open toward right)
            let leftArc = NSBezierPath()
            leftArc.lineWidth = lineWidth
            leftArc.lineCapStyle = .round
            leftArc.appendArc(withCenter: leftCenter, radius: r, startAngle: 42, endAngle: 318, clockwise: false)
            leftArc.stroke()

            // Right Colab loop (mirrored C-arc open toward left)
            let rightArc = NSBezierPath()
            rightArc.lineWidth = lineWidth
            rightArc.lineCapStyle = .round
            rightArc.appendArc(withCenter: rightCenter, radius: r, startAngle: 222, endAngle: 138, clockwise: false)
            rightArc.stroke()

            // Center interlock connector
            let bridge = NSBezierPath()
            bridge.lineWidth = lineWidth
            bridge.lineCapStyle = .round
            bridge.move(to: NSPoint(x: leftCenter.x + r * 0.72, y: cy + r * 0.65))
            bridge.line(to: NSPoint(x: rightCenter.x - r * 0.72, y: cy - r * 0.65))
            bridge.stroke()

            // Active status dot in bottom-right corner when running/processing
            if isRunning {
                let dotSize: CGFloat = isProcessing ? 4.0 : 3.2
                let dotRect = NSRect(x: rect.maxX - dotSize - 0.5, y: 1.5, width: dotSize, height: dotSize)
                NSBezierPath(ovalIn: dotRect).fill()
            }
            return true
        }
        image.isTemplate = true
        return image
    }
}
