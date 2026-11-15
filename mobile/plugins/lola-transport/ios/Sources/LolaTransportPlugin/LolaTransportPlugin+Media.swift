import Capacitor
import Foundation
import LolaTransportCore
import PhotosUI
import UIKit

extension LolaTransportPlugin {
    /// Takes a photo or picks one from the library. Cancelling resolves
    /// `{cancelled: true}`. The image is downscaled to `MAX_PICK_LONG_EDGE_PX`
    /// and encoded as JPEG (q 0.82, then 0.6) to fit `MAX_PICK_BYTES`; one that
    /// still does not fit is refused with `too_large`.
    @objc func pickImage(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        guard let source = call.getString("source"), source == "camera" || source == "library" else {
            return call.reject("source must be camera or library", FailureCode.internalError.rawValue)
        }
        DispatchQueue.main.async { [weak self] in
            guard let presenter = self?.bridge?.viewController else {
                return call.reject("nothing to present on", FailureCode.internalError.rawValue)
            }
            if source == "camera", !UIImagePickerController.isSourceTypeAvailable(.camera) {
                return call.reject("no camera", "no_camera")
            }
            LolaImagePicker.present(camera: source == "camera", from: presenter) { image in
                guard let image else { return call.resolve(["cancelled": true]) }
                DispatchQueue.global(qos: .userInitiated).async {
                    guard let encoded = LolaImagePicker.encode(image) else {
                        return call.reject("the image is too large", "too_large")
                    }
                    call.resolve([
                        "cancelled": false, "mime": "image/jpeg", "data": encoded.data.base64EncodedString(),
                        "width": encoded.width, "height": encoded.height,
                    ])
                }
            }
        }
    }

    /// The clipboard's image, if it has one. `hasImages` first: reading the
    /// image itself shows the system paste banner, which an empty clipboard
    /// should not.
    @objc func readClipboardImage(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        DispatchQueue.main.async {
            let board = UIPasteboard.general
            guard board.hasImages else { return call.resolve(["present": false]) }
            let data: Data?
            let mime: String
            if let jpeg = board.data(forPasteboardType: "public.jpeg") {
                data = jpeg
                mime = "image/jpeg"
            } else {
                data = board.image?.pngData()
                mime = "image/png"
            }
            guard let data else { return call.resolve(["present": false]) }
            guard data.count <= Limits.maxClipboardBytes else {
                return call.reject("the image is too large", "too_large")
            }
            call.resolve(["present": true, "mime": mime, "data": data.base64EncodedString()])
        }
    }
}

/// Presents the camera or the photo library and returns the chosen image.
/// Retains itself until the presentation ends.
final class LolaImagePicker: NSObject, UIImagePickerControllerDelegate, UINavigationControllerDelegate,
    PHPickerViewControllerDelegate
{
    private static var current: LolaImagePicker?
    private let completion: (UIImage?) -> Void

    private init(completion: @escaping (UIImage?) -> Void) { self.completion = completion }

    static func present(camera: Bool, from presenter: UIViewController, completion: @escaping (UIImage?) -> Void) {
        let picker = LolaImagePicker(completion: completion)
        current = picker
        if camera {
            let controller = UIImagePickerController()
            controller.sourceType = .camera
            controller.delegate = picker
            presenter.present(controller, animated: true)
        } else {
            var configuration = PHPickerConfiguration()
            configuration.filter = .images
            configuration.selectionLimit = 1
            let controller = PHPickerViewController(configuration: configuration)
            controller.delegate = picker
            presenter.present(controller, animated: true)
        }
    }

    private func finish(_ image: UIImage?) {
        Self.current = nil
        completion(image)
    }

    func imagePickerController(
        _ picker: UIImagePickerController, didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]
    ) {
        let image = info[.originalImage] as? UIImage
        picker.dismiss(animated: true) { self.finish(image) }
    }

    func imagePickerControllerDidCancel(_ picker: UIImagePickerController) {
        picker.dismiss(animated: true) { self.finish(nil) }
    }

    func picker(_ picker: PHPickerViewController, didFinishPicking results: [PHPickerResult]) {
        picker.dismiss(animated: true)
        guard let provider = results.first?.itemProvider, provider.canLoadObject(ofClass: UIImage.self) else {
            return finish(nil)
        }
        provider.loadObject(ofClass: UIImage.self) { object, _ in
            DispatchQueue.main.async { self.finish(object as? UIImage) }
        }
    }

    /// Downscales to the long-edge limit and encodes as JPEG within `MAX_PICK_BYTES`.
    static func encode(_ image: UIImage) -> (data: Data, width: Int, height: Int)? {
        let longest = max(image.size.width, image.size.height) * image.scale
        let ratio = min(1, CGFloat(Limits.maxPickLongEdgePx) / max(longest, 1))
        let size = CGSize(
            width: (image.size.width * image.scale * ratio).rounded(), height: (image.size.height * image.scale * ratio).rounded())
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        format.opaque = true
        let scaled = UIGraphicsImageRenderer(size: size, format: format).image { _ in
            UIColor.white.setFill()
            UIRectFill(CGRect(origin: .zero, size: size))
            image.draw(in: CGRect(origin: .zero, size: size))
        }
        for quality in [0.82, 0.6] as [CGFloat] {
            if let data = scaled.jpegData(compressionQuality: quality), data.count <= Limits.maxPickBytes {
                return (data, Int(size.width), Int(size.height))
            }
        }
        return nil
    }
}
