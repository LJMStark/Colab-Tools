import Foundation

/// Supported compute workload profiles in Colab Tools.
/// Hardware telemetry (CU balance, GPU, VRAM, uptime) is universal; workload-specific UI cards,
/// WebUI/tunnel ports, and idle watchdog signal sources adapt automatically to this mode.
public enum WorkloadKind: String, CaseIterable, Codable, Identifiable, Sendable {
    case llm = "llm"
    case video = "video"
    case image = "image"
    case audio = "audio"
    case music = "music"
    case general = "general"

    public var id: String { rawValue }

    public var displayName: String {
        switch self {
        case .llm: return "LLM 大模型"
        case .video: return "视频生成"
        case .image: return "图像生成"
        case .audio: return "音频 / 语音"
        case .music: return "音乐生成"
        case .general: return "训练 / 通用"
        }
    }

    public var shortBadge: String {
        switch self {
        case .llm: return "LLM"
        case .video: return "视频"
        case .image: return "生图"
        case .audio: return "音频"
        case .music: return "音乐"
        case .general: return "通用"
        }
    }

    public var iconName: String {
        switch self {
        case .llm: return "square.stack.3d.up"
        case .video: return "film.stack"
        case .image: return "photo.stack"
        case .audio: return "waveform"
        case .music: return "music.note.list"
        case .general: return "server.rack"
        }
    }

    public var metricCardTitle: String {
        switch self {
        case .llm: return "上下文"
        case .video: return "渲染队列"
        case .image: return "出图队列"
        case .audio: return "音频服务"
        case .music: return "音轨任务"
        case .general: return "整机负载"
        }
    }

    public var defaultRemotePort: Int {
        switch self {
        case .llm: return 18080
        case .video, .image: return 8188
        case .audio, .music: return 7860
        case .general: return 8888
        }
    }

    public var defaultLocalTunnelPort: Int {
        defaultRemotePort
    }

    public var supportsWebUI: Bool {
        switch self {
        case .video, .image, .audio, .music: return true
        case .llm, .general: return false
        }
    }

    public var webUIButtonTitle: String {
        switch self {
        case .audio, .music: return "试听页"
        case .video, .image: return "WebUI"
        case .llm, .general: return "网页"
        }
    }

    public var defaultScriptHint: String {
        switch self {
        case .llm: return "/content/drive/MyDrive/colab-llm/scripts/start_server.sh"
        case .video, .image: return "/content/drive/MyDrive/colab-tools/comfyui-video.sh"
        case .audio, .music: return "/content/drive/MyDrive/colab-tools/gradio-audio-music.sh"
        case .general: return ""
        }
    }
}

/// Identifies the authoritative signal currently driving the idle auto-shutdown watchdog.
public enum ActivitySignalSource: String, Equatable, Sendable {
    case none
    case llmSlots
    case statusFile
    case comfyQueue
    case hardwareDualLowLoad

    public var shortLabel: String {
        switch self {
        case .none: return "未接入信号"
        case .llmSlots: return "/slots 信号"
        case .statusFile: return "状态协议"
        case .comfyQueue: return "Comfy 队列"
        case .hardwareDualLowLoad: return "GPU+CPU 双低载"
        }
    }
}

/// Open JSON schema written by any remote workload script to `/tmp/colab-tools-status.json`.
public struct WorkloadStatusPayload: Decodable, Equatable, Sendable {
    public let kind: String?
    public let name: String?
    public let uiURL: String?
    public let port: Int?
    public let metricLabel: String?
    public let metricValue: String?
    public let metricUnit: String?
    public let metricRatio: Double?
    public let busy: Bool?
    public let queueRunning: Int?
    public let queuePending: Int?
    public let detail: String?

    enum CodingKeys: String, CodingKey {
        case kind
        case name
        case uiURL = "ui_url"
        case port
        case metricLabel = "metric_label"
        case metricValue = "metric_value"
        case metricUnit = "metric_unit"
        case metricRatio = "metric_ratio"
        case busy
        case queueRunning = "queue_running"
        case queuePending = "queue_pending"
        case detail
    }

    public var parsedKind: WorkloadKind? {
        guard let k = kind?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() else { return nil }
        return WorkloadKind(rawValue: k)
    }

    public var isWorkloadBusy: Bool? {
        if let busy { return busy }
        if let r = queueRunning, let p = queuePending { return (r + p) > 0 }
        if let r = queueRunning { return r > 0 }
        if let p = queuePending { return p > 0 }
        return nil
    }
}

/// Summary extracted from ComfyUI's native `GET /queue` JSON response.
public struct ComfyQueueSummary: Equatable, Sendable {
    public let running: Int
    public let pending: Int

    public init(running: Int, pending: Int) {
        self.running = max(0, running)
        self.pending = max(0, pending)
    }

    public var isBusy: Bool {
        running > 0 || pending > 0
    }
}

public enum WorkloadStatusParser {
    public static func parseStatusJSON(_ raw: String) -> WorkloadStatusPayload? {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let data = trimmed.data(using: .utf8) else { return nil }
        return try? JSONDecoder().decode(WorkloadStatusPayload.self, from: data)
    }

    public static func parseComfyQueueJSON(_ raw: String) -> ComfyQueueSummary? {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty,
              let data = trimmed.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        guard let runningArr = obj["queue_running"] as? [Any],
              let pendingArr = obj["queue_pending"] as? [Any] else {
            return nil
        }
        return ComfyQueueSummary(running: runningArr.count, pending: pendingArr.count)
    }
}
