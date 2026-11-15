import Foundation
import LolaTransportCore

/// The phone's half of a relay: a `URLSessionWebSocketTask` to the hub (03 §13.2).
///
/// URLSession, not `NWProtocolWebSocket`, because only URLSession exposes the
/// status and headers of a refused upgrade, which is what classifies a relay
/// failure. The session is ephemeral (no cache, no cookie store) and honours
/// the system trust store and proxy.
final class RelayLeg: NSObject, URLSessionWebSocketDelegate {
    enum OpenError: Error {
        case refused(RelayRefusal)
        case network(String)
    }

    private let request: URLRequest
    private let queue: DispatchQueue
    private var session: URLSession?
    private var task: URLSessionWebSocketTask?
    private var opened = false
    private var finished = false
    private var openCompletion: ((Result<Void, OpenError>) -> Void)?
    private var upgradeTimer: DispatchWorkItem?

    /// A leg that was open and has ended (any close code is an ordinary drop).
    var onClosed: (() -> Void)?

    /// `queue` must be serial: it becomes the session's delegate queue.
    init(request: URLRequest, queue: DispatchQueue) {
        self.request = request
        self.queue = queue
    }

    /// Resolves on the 101, or with the hub's refusal. The 101 alone proves the
    /// daemon dialled its accept leg.
    func open(_ completion: @escaping (Result<Void, OpenError>) -> Void) {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.urlCache = nil
        configuration.httpCookieStorage = nil
        configuration.httpShouldSetCookies = false
        configuration.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        configuration.timeoutIntervalForRequest = RelayWire.idleTimeout

        let delegateQueue = OperationQueue()
        delegateQueue.maxConcurrentOperationCount = 1
        delegateQueue.underlyingQueue = queue
        let session = URLSession(configuration: configuration, delegate: self, delegateQueue: delegateQueue)
        let task = session.webSocketTask(with: request)
        task.maximumMessageSize = LolaFrameLimits.maxFrameBytes
        self.session = session
        self.task = task
        openCompletion = completion

        let timer = DispatchWorkItem { [weak self] in self?.failOpen(.refused(.timeout)) }
        upgradeTimer = timer
        queue.asyncAfter(deadline: .now() + RelayWire.upgradeTimeout, execute: timer)
        task.resume()
    }

    func send(_ chunk: Data, completion: @escaping (Error?) -> Void) {
        guard let task, !finished else { return completion(URLError(.networkConnectionLost)) }
        task.send(.data(chunk)) { [queue] error in queue.async { completion(error) } }
    }

    /// One binary message. A text message is a protocol violation.
    func receive(_ completion: @escaping (Result<Data, Error>) -> Void) {
        guard let task, !finished else { return completion(.failure(URLError(.networkConnectionLost))) }
        task.receive { [queue] result in
            queue.async {
                switch result {
                case .success(.data(let data)): completion(.success(data))
                case .success: completion(.failure(URLError(.cannotParseResponse)))
                case .failure(let error): completion(.failure(error))
                }
            }
        }
    }

    func cancel() {
        finished = true
        openCompletion = nil
        onClosed = nil
        upgradeTimer?.cancel()
        task?.cancel(with: .goingAway, reason: nil)
        session?.invalidateAndCancel()
        task = nil
        session = nil
    }

    // MARK: URLSessionWebSocketDelegate

    func urlSession(
        _ session: URLSession, webSocketTask: URLSessionWebSocketTask, didOpenWithProtocol protocol: String?
    ) {
        guard !finished, !opened else { return }
        opened = true
        upgradeTimer?.cancel()
        let completion = openCompletion
        openCompletion = nil
        completion?(.success(()))
    }

    func urlSession(
        _ session: URLSession, webSocketTask: URLSessionWebSocketTask,
        didCloseWith closeCode: URLSessionWebSocketTask.CloseCode, reason: Data?
    ) {
        ended()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        guard !finished else { return }
        if opened { return ended() }
        guard let response = task.response as? HTTPURLResponse, response.statusCode != 101 else {
            let timedOut = (error as? URLError)?.code == .timedOut
            return failOpen(timedOut ? .refused(.timeout) : .network(error?.localizedDescription ?? "closed"))
        }
        failOpen(
            .refused(
                RelayWire.refusal(
                    status: response.statusCode,
                    code: response.value(forHTTPHeaderField: RelayWire.codeHeader),
                    retryAfter: response.value(forHTTPHeaderField: "Retry-After"))))
    }

    private func failOpen(_ error: OpenError) {
        guard !finished, let completion = openCompletion else { return }
        openCompletion = nil
        upgradeTimer?.cancel()
        cancel()
        completion(.failure(error))
    }

    private func ended() {
        guard !finished else { return }
        let callback = onClosed
        cancel()
        callback?()
    }
}
