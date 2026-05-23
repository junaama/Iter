import SwiftUI

struct RootSessionView: View {
    @Environment(SessionStore.self) private var sessionStore

    var body: some View {
        switch sessionStore.status {
        case .loading:
            LoadingSessionView()
        case .signedIn:
            WorkspaceView()
        case .signedOut, .signingIn, .polling, .expired, .failed:
            SignInView()
        }
    }
}

private struct LoadingSessionView: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        ZStack {
            Color.iterStageBackdrop(for: colorScheme)
                .ignoresSafeArea()
            ProgressView()
                .controlSize(.small)
        }
    }
}

struct SignInView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(SessionStore.self) private var sessionStore

    var body: some View {
        ZStack {
            Color.iterStageBackdrop(for: colorScheme)
                .ignoresSafeArea()

            VStack(alignment: .leading, spacing: IterSpacing.gapLarge) {
                SignInHeader()

                VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
                    HStack(alignment: .top) {
                        VStack(alignment: .leading, spacing: 5) {
                            Text(verbatim: "WorkOS device sign-in")
                                .font(IterFont.sansSectionTitle)
                                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                            Text(verbatim: statusCopy)
                                .font(IterFont.sansSmall)
                                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                                .fixedSize(horizontal: false, vertical: true)
                        }

                        Spacer()

                        if sessionStore.status == .signingIn || sessionStore.status == .polling {
                            ProgressView()
                                .controlSize(.small)
                        }
                    }

                    if let authorization = sessionStore.deviceAuthorization {
                        DeviceCodeBlock(authorization: authorization)

                        HStack(spacing: IterSpacing.gapSmall) {
                            ButtonPrimary(title: "Open in browser") {
                                sessionStore.openVerificationURL()
                            }
                            IterButton(title: "Cancel") {
                                sessionStore.signOut()
                            }
                        }
                    } else {
                        ButtonPrimary(title: "Sign in") {
                            sessionStore.startDeviceAuthorization()
                        }
                    }

                    if let message = sessionStore.lastError {
                        Text(verbatim: message)
                            .font(IterFont.monoSmall)
                            .foregroundStyle(Color.iterBad(for: colorScheme))
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .padding(IterSpacing.gapLarge)
                .frame(width: 420, alignment: .leading)
                .background(Color.iterPanel(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.card))
                .overlay {
                    RoundedRectangle(cornerRadius: IterRadius.card)
                        .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
                }
            }
            .padding(IterSpacing.gapLarge)
        }
        .frame(minWidth: 680, minHeight: 520)
    }

    private var statusCopy: String {
        switch sessionStore.status {
        case .expired:
            return "Session expired. Sign in again to continue."
        case .failed:
            return "The previous sign-in attempt failed."
        case .polling:
            return "Complete the browser step. Iter will continue automatically."
        case .signingIn:
            return "Requesting a device code."
        default:
            return "Sign in to sync traces, scores, and prompt refinements with your team."
        }
    }
}

private struct SignInHeader: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            Text(verbatim: "i")
                .font(IterFont.monoAvatar)
                .foregroundStyle(Color.white)
                .frame(width: 26, height: 26)
                .background(Color.iterAccent(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.avatar))

            Text(verbatim: "iter")
                .font(IterFont.monoTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
        }
    }
}

private struct DeviceCodeBlock: View {
    @Environment(\.colorScheme) private var colorScheme

    let authorization: DeviceAuthorization

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            Text(verbatim: authorization.userCode)
                .font(IterFont.mono(size: 24, weight: .semibold))
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .tracking(1)

            Text(verbatim: authorization.verificationURI.absoluteString)
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .padding(IterSpacing.gapMedium)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.iterSidebar(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.card)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Device code \(authorization.userCode)")
    }
}

#Preview("Sign In") {
    SignInView()
        .environment(SessionStore())
        .preferredColorScheme(.light)
}
