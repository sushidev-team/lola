import AVFoundation
import Foundation
import UIKit

/// The QR scanner: a camera, a metadata output restricted to QR symbols, and
/// one string handed back.
///
/// It is in this file and nowhere near `LolaConnection` on purpose. The two
/// share no state, no queue and no failure vocabulary: the socket's failures are
/// about a peer that is unreachable or refusing, and the scanner's are about a
/// camera that does not exist or a permission a human declined. Folding them
/// together would produce an enum where half the values are unreachable from
/// half the callers.
///
/// What this deliberately does NOT do is understand the payload. The scanner
/// returns the decoded string exactly as the symbol carried it, and the app
/// decides whether it is a pairing token, a stale QR from another product, or
/// noise. The plugin owning the transport should not also own the enrolment
/// format, which is still being written; and a scanner that silently rejects an
/// unfamiliar string is much harder to diagnose from a phone than one that
/// reports what it saw.
///
/// It also does not touch the app's WebView. The common community
/// implementation of an in-app scanner makes the WKWebView transparent and puts
/// the camera preview layer behind it, which means every exit path has to undo
/// that exactly, and it interacts with this app's `contentInset: "never"` and
/// `scrollEnabled: false`. A modally presented view controller owns its own
/// window space and leaves the WebView alone.

/// Why a scan could not happen. Distinct from `LolaFailureCode`, which is about
/// the socket.
public enum LolaScanFailureCode: String, Equatable, Sendable {
    /// There is no capture device at all. The iOS Simulator is the case that
    /// matters day to day: it has no camera and cannot be given one, so a build
    /// under test can never scan and a UI that offers the button anyway is
    /// offering a dead control. Ask `LolaQRScanner.availability()` before
    /// drawing the button rather than waiting for this on a tap.
    case noCamera = "no_camera"

    /// The human declined camera access. iOS asks exactly once; after a refusal
    /// the only route back is Settings, so this must be reported as its own
    /// state and not folded into a generic failure.
    case denied = "camera_denied"

    /// Camera access is disallowed by policy (Screen Time, MDM). Not the same
    /// as `denied`: there is no Settings toggle the user can flip.
    case restricted = "camera_restricted"

    /// There was nothing to present the scanner on, or the capture graph could
    /// not be assembled. A defect or a very unusual device, either way not
    /// something the user can act on.
    case unavailable
}

public struct LolaScanFailure: Equatable, Sendable {
    public let code: LolaScanFailureCode
    /// One short human line. Never carries anything the camera saw.
    public let reason: String

    public init(code: LolaScanFailureCode, reason: String) {
        self.code = code
        self.reason = reason
    }
}

/// The three ways a scan ends. Cancellation is a separate case rather than a
/// failure because it is the ordinary outcome of a human changing their mind,
/// and rendering it as an error would put a red banner on a normal action.
public enum LolaScanOutcome: Equatable, Sendable {
    case value(String)
    case cancelled
    case failed(LolaScanFailure)
}

/// What the app needs to know BEFORE it draws a Scan button.
public struct LolaScanAvailability: Equatable, Sendable {
    public let available: Bool
    /// `notDetermined`, `authorized`, `denied` or `restricted`. Reported even
    /// when `available` is false for a different reason, because "there is no
    /// camera" and "the camera was refused" need different words on screen.
    public let authorization: String
    /// Set when `available` is false.
    public let reason: LolaScanFailureCode?

    public init(available: Bool, authorization: String, reason: LolaScanFailureCode?) {
        self.available = available
        self.authorization = authorization
        self.reason = reason
    }
}

public enum LolaQRScanner {
    /// Whether a scan could succeed on this device right now.
    ///
    /// `notDetermined` counts as available: the prompt has not been shown yet,
    /// and hiding the button would guarantee it never is. The permission is
    /// requested when the scanner opens, which is the moment a human has just
    /// expressed the intent that makes the prompt make sense.
    public static func availability() -> LolaScanAvailability {
        let status = AVCaptureDevice.authorizationStatus(for: .video)
        let name = authorizationName(status)

        switch status {
        case .denied:
            return LolaScanAvailability(available: false, authorization: name, reason: .denied)
        case .restricted:
            return LolaScanAvailability(available: false, authorization: name, reason: .restricted)
        default:
            break
        }

        guard captureDevice() != nil else {
            return LolaScanAvailability(available: false, authorization: name, reason: .noCamera)
        }
        return LolaScanAvailability(available: true, authorization: name, reason: nil)
    }

    /// Presents the scanner over `presenter` and reports exactly one outcome.
    ///
    /// The completion runs on the main queue and runs once: the controller
    /// guards against a second metadata callback arriving between the first
    /// match and the session actually stopping, which is a real race rather than
    /// a theoretical one because `stopRunning` is asynchronous with respect to
    /// the delegate queue.
    public static func present(
        from presenter: UIViewController?,
        prompt: String?,
        completion: @escaping (LolaScanOutcome) -> Void
    ) {
        guard let presenter else {
            completion(.failed(LolaScanFailure(
                code: .unavailable,
                reason: "no view controller is available to present the scanner")))
            return
        }

        let status = AVCaptureDevice.authorizationStatus(for: .video)
        switch status {
        case .authorized:
            open(from: presenter, prompt: prompt, completion: completion)
        case .notDetermined:
            AVCaptureDevice.requestAccess(for: .video) { granted in
                DispatchQueue.main.async {
                    guard granted else {
                        completion(.failed(LolaScanFailure(
                            code: .denied,
                            reason: "camera access was declined")))
                        return
                    }
                    open(from: presenter, prompt: prompt, completion: completion)
                }
            }
        case .denied:
            completion(.failed(LolaScanFailure(
                code: .denied,
                reason: "camera access is off for Lola in Settings")))
        case .restricted:
            completion(.failed(LolaScanFailure(
                code: .restricted,
                reason: "camera access is not permitted on this device")))
        @unknown default:
            completion(.failed(LolaScanFailure(
                code: .unavailable,
                reason: "camera authorization is in an unrecognised state")))
        }
    }

    private static func open(
        from presenter: UIViewController,
        prompt: String?,
        completion: @escaping (LolaScanOutcome) -> Void
    ) {
        guard let device = captureDevice() else {
            completion(.failed(LolaScanFailure(
                code: .noCamera,
                reason: "this device has no camera; the Simulator never does")))
            return
        }

        let controller = LolaQRScannerController(device: device, prompt: prompt)
        controller.onOutcome = { [weak controller] outcome in
            controller?.dismiss(animated: true) {
                completion(outcome)
            }
        }
        // Full screen rather than a sheet: a sheet can be swiped away, which
        // would be a fourth exit path to keep in step with the other three, and
        // a camera viewfinder in a card reads as a preview rather than as the
        // task.
        controller.modalPresentationStyle = .fullScreen
        presenter.present(controller, animated: true)
    }

    private static func captureDevice() -> AVCaptureDevice? {
        AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .back)
            ?? AVCaptureDevice.default(for: .video)
    }

    private static func authorizationName(_ status: AVAuthorizationStatus) -> String {
        switch status {
        case .notDetermined: return "notDetermined"
        case .restricted: return "restricted"
        case .denied: return "denied"
        case .authorized: return "authorized"
        @unknown default: return "notDetermined"
        }
    }
}

/// The viewfinder.
///
/// Locked to portrait deliberately. The alternative is tracking the interface
/// orientation onto the preview connection, which on iOS 17 means the
/// `videoRotationAngle` API and on earlier ones the deprecated
/// `videoOrientation`, for a screen whose entire content is a square target a
/// human points at a laptop. Portrait removes the question.
final class LolaQRScannerController: UIViewController {
    var onOutcome: ((LolaScanOutcome) -> Void)?

    private let device: AVCaptureDevice
    private let prompt: String?
    private let session = AVCaptureSession()
    private let relay = MetadataRelay()
    /// `startRunning` and `stopRunning` block, so they never run on the main
    /// queue; everything else about the session is configured before it starts.
    private let sessionQueue = DispatchQueue(label: "dev.sushi.lola.mobile.scanner")
    private var previewLayer: AVCaptureVideoPreviewLayer?
    private var finished = false

    init(device: AVCaptureDevice, prompt: String?) {
        self.device = device
        self.prompt = prompt
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("LolaQRScannerController is created in code only")
    }

    override var supportedInterfaceOrientations: UIInterfaceOrientationMask { .portrait }
    override var preferredInterfaceOrientationForPresentation: UIInterfaceOrientation { .portrait }
    override var prefersStatusBarHidden: Bool { true }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black

        guard buildSession() else {
            finish(.failed(LolaScanFailure(
                code: .unavailable,
                reason: "the camera could not be configured")))
            return
        }

        let preview = AVCaptureVideoPreviewLayer(session: session)
        preview.videoGravity = .resizeAspectFill
        preview.frame = view.bounds
        view.layer.insertSublayer(preview, at: 0)
        previewLayer = preview

        addChrome()

        relay.onCode = { [weak self] code in
            self?.finish(.value(code))
        }
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        previewLayer?.frame = view.bounds
    }

    override func viewWillAppear(_ animated: Bool) {
        super.viewWillAppear(animated)
        let session = self.session
        sessionQueue.async {
            if !session.isRunning { session.startRunning() }
        }
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        let session = self.session
        sessionQueue.async {
            if session.isRunning { session.stopRunning() }
        }
    }

    private func buildSession() -> Bool {
        session.beginConfiguration()
        defer { session.commitConfiguration() }

        guard let input = try? AVCaptureDeviceInput(device: device),
              session.canAddInput(input)
        else { return false }
        session.addInput(input)

        let output = AVCaptureMetadataOutput()
        guard session.canAddOutput(output) else { return false }
        session.addOutput(output)

        // The delegate queue is main, so the relay never has to hop, and the
        // types must be set AFTER the output is added to a session or the
        // available-types list is still empty and this assignment traps.
        output.setMetadataObjectsDelegate(relay, queue: .main)
        guard output.availableMetadataObjectTypes.contains(.qr) else { return false }
        output.metadataObjectTypes = [.qr]
        return true
    }

    private func addChrome() {
        let target = UIView()
        target.translatesAutoresizingMaskIntoConstraints = false
        target.layer.borderColor = UIColor.white.withAlphaComponent(0.9).cgColor
        target.layer.borderWidth = 2
        target.layer.cornerRadius = 16
        target.backgroundColor = .clear
        view.addSubview(target)

        let hint = UILabel()
        hint.translatesAutoresizingMaskIntoConstraints = false
        hint.text = prompt ?? "Point the camera at the QR code in the lola desktop app."
        hint.textColor = .white
        hint.numberOfLines = 0
        hint.textAlignment = .center
        hint.font = .preferredFont(forTextStyle: .body)
        hint.adjustsFontForContentSizeCategory = true
        hint.shadowColor = UIColor.black.withAlphaComponent(0.6)
        hint.shadowOffset = CGSize(width: 0, height: 1)
        view.addSubview(hint)

        // UIButton.Configuration rather than the setTitle/contentEdgeInsets
        // pair: those were deprecated in iOS 15, which is this package's
        // deployment target, and the insets in particular are silently ignored
        // once a configuration exists.
        var style = UIButton.Configuration.filled()
        style.title = "Cancel"
        style.baseBackgroundColor = UIColor.black.withAlphaComponent(0.55)
        style.baseForegroundColor = .white
        style.cornerStyle = .medium
        style.contentInsets = NSDirectionalEdgeInsets(
            top: 12, leading: 24, bottom: 12, trailing: 24)

        let cancel = UIButton(configuration: style)
        cancel.translatesAutoresizingMaskIntoConstraints = false
        cancel.titleLabel?.adjustsFontForContentSizeCategory = true
        cancel.addTarget(self, action: #selector(cancelTapped), for: .touchUpInside)
        view.addSubview(cancel)

        let guide = view.safeAreaLayoutGuide
        NSLayoutConstraint.activate([
            target.centerXAnchor.constraint(equalTo: view.centerXAnchor),
            target.centerYAnchor.constraint(equalTo: view.centerYAnchor, constant: -24),
            target.widthAnchor.constraint(equalTo: view.widthAnchor, multiplier: 0.72),
            target.heightAnchor.constraint(equalTo: target.widthAnchor),

            hint.leadingAnchor.constraint(equalTo: guide.leadingAnchor, constant: 24),
            hint.trailingAnchor.constraint(equalTo: guide.trailingAnchor, constant: -24),
            hint.topAnchor.constraint(equalTo: target.bottomAnchor, constant: 24),

            cancel.centerXAnchor.constraint(equalTo: view.centerXAnchor),
            cancel.bottomAnchor.constraint(equalTo: guide.bottomAnchor, constant: -32),
            // A 44 point minimum is the platform's tap target, and this control
            // is the only way out of a full-screen camera.
            cancel.heightAnchor.constraint(greaterThanOrEqualToConstant: 44),
        ])
    }

    @objc private func cancelTapped() {
        finish(.cancelled)
    }

    /// Reports exactly one outcome, whatever order the camera, the button and
    /// the dismissal arrive in.
    private func finish(_ outcome: LolaScanOutcome) {
        guard !finished else { return }
        finished = true
        relay.onCode = nil
        let handler = onOutcome
        onOutcome = nil
        handler?(outcome)
    }
}

/// The delegate, split out so the view controller does not have to satisfy an
/// Objective-C protocol requirement from a main-actor-isolated class.
///
/// It holds a closure and nothing else. The output is configured with the main
/// queue, so the closure is already on the main thread when it runs.
final class MetadataRelay: NSObject, AVCaptureMetadataOutputObjectsDelegate {
    var onCode: ((String) -> Void)?

    func metadataOutput(
        _ output: AVCaptureMetadataOutput,
        didOutput metadataObjects: [AVMetadataObject],
        from connection: AVCaptureConnection
    ) {
        guard let handler = onCode else { return }
        for object in metadataObjects {
            guard let code = object as? AVMetadataMachineReadableCodeObject,
                  code.type == .qr,
                  let value = code.stringValue,
                  !value.isEmpty
            else { continue }
            handler(value)
            return
        }
    }
}
