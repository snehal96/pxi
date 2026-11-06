// Draws pxi's app icon into an .iconset folder: swift make-icon.swift <out.iconset>
import AppKit

let out = URL(fileURLWithPath: CommandLine.arguments[1])
try? FileManager.default.createDirectory(at: out, withIntermediateDirectories: true)

func draw(_ px: Int) -> Data {
    let s = CGFloat(px)
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px, bitsPerSample: 8,
                               samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                               colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    // macOS icon grid: 824/1024 body with rounded corners.
    let inset = s * 100 / 1024
    let body = NSRect(x: inset, y: inset, width: s - 2 * inset, height: s - 2 * inset)
    let path = NSBezierPath(roundedRect: body, xRadius: body.width * 0.225, yRadius: body.width * 0.225)
    NSGradient(colors: [NSColor(srgbRed: 0.10, green: 0.11, blue: 0.15, alpha: 1),
                        NSColor(srgbRed: 0.05, green: 0.06, blue: 0.08, alpha: 1)])!.draw(in: path, angle: -90)
    // Three offset "worktree" bars.
    let bar = body.width * 0.075
    let colors = [NSColor(srgbRed: 0.36, green: 0.80, blue: 0.56, alpha: 1),
                  NSColor(srgbRed: 0.40, green: 0.62, blue: 0.98, alpha: 1),
                  NSColor(srgbRed: 0.98, green: 0.70, blue: 0.32, alpha: 1)]
    for (i, c) in colors.enumerated() {
        let y = body.minY + body.height * 0.17 + CGFloat(i) * bar * 1.7
        let x = body.minX + body.width * (0.18 + CGFloat(i) * 0.06)
        let r = NSRect(x: x, y: y, width: body.width * (0.46 - CGFloat(i) * 0.06), height: bar)
        c.setFill()
        NSBezierPath(roundedRect: r, xRadius: bar / 2, yRadius: bar / 2).fill()
    }
    let font = NSFont.monospacedSystemFont(ofSize: body.height * 0.30, weight: .bold)
    let text = NSAttributedString(string: "pxi", attributes: [.font: font, .foregroundColor: NSColor.white])
    let size = text.size()
    text.draw(at: NSPoint(x: body.midX - size.width / 2, y: body.minY + body.height * 0.48))
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

for base in [16, 32, 128, 256, 512] {
    try! draw(base).write(to: out.appendingPathComponent("icon_\(base)x\(base).png"))
    try! draw(base * 2).write(to: out.appendingPathComponent("icon_\(base)x\(base)@2x.png"))
}
