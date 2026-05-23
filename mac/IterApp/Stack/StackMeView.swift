import SwiftUI
// swiftlint:disable file_length

struct StackMeView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Bindable var store: StackStore

    @State private var isShareSheetPresented = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: IterSpacing.sectionGap) {
                StackHeaderView(store: store, isShareSheetPresented: $isShareSheetPresented)

                StackHarnessSection(harnesses: store.stack.harnesses)

                StackEditableListSection(
                    title: "Skills",
                    emptyTitle: "No skills",
                    addButtonTitle: "Add skill",
                    addAction: store.addSkill
                ) {
                    VStack(spacing: 6) {
                        ForEach($store.stack.skills) { $skill in
                            StackSkillRow(skill: $skill) {
                                store.removeSkill(skill)
                            }
                        }
                        StackSkillEntryRow(
                            name: $store.pendingSkillName,
                            sourcePath: $store.pendingSkillSource,
                            addAction: store.addSkill
                        )
                    }
                }

                StackEditableListSection(
                    title: "Doc references",
                    emptyTitle: "No doc references",
                    addButtonTitle: "Add reference",
                    addAction: store.addDocReference
                ) {
                    VStack(spacing: 6) {
                        ForEach($store.stack.docs) { $reference in
                            StackDocRow(reference: $reference) {
                                store.removeDocReference(reference)
                            }
                        }
                        StackDocEntryRow(reference: $store.pendingDocReference) {
                            store.addDocReference()
                        }
                    }
                }

                StackNotesSection(notes: $store.stack.notes)
            }
            .padding(IterSpacing.mainPanePadding)
        }
        .background(Color.iterPanel(for: colorScheme))
        .safeAreaInset(edge: .bottom) {
            StackToastView(toast: store.toast)
                .padding(.bottom, IterSpacing.gapMedium)
        }
        .task {
            await store.load()
        }
        .sheet(isPresented: $isShareSheetPresented) {
            StackMeShareSheet(store: store)
        }
    }
}

private struct StackHeaderView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Bindable var store: StackStore
    @Binding var isShareSheetPresented: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: IterSpacing.gapSmall) {
                        TextField("Stack name", text: $store.stack.name)
                            .textFieldStyle(.plain)
                            .font(IterFont.sansKPIValue)
                            .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                        StatusPillView(label: store.statusLabel)
                    }

                    if case .offlineDraft(let message) = store.loadState {
                        Text(verbatim: message)
                            .font(IterFont.monoSmall)
                            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                            .lineLimit(1)
                    } else if store.stack.isDraft {
                        Text(verbatim: "Detected draft")
                            .font(IterFont.monoSmall)
                            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    }
                }

                Spacer()

                HStack(spacing: IterSpacing.gapSmall) {
                    IterButton(title: "Share") {
                        isShareSheetPresented = true
                    }
                    ButtonPrimary(title: store.isSaving ? "Saving" : "Save") {
                        Task { await store.save() }
                    }
                    .disabled(store.isSaving)
                }
            }
        }
    }
}

private struct StackHarnessSection: View {
    @Environment(\.colorScheme) private var colorScheme

    let harnesses: [StackHarness]

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            StackSectionTitle(title: "Harnesses", detail: "\(harnesses.count)")
            HStack(spacing: IterSpacing.gapSmall) {
                ForEach(harnesses) { harness in
                    StackHarnessChip(harness: harness)
                }
            }
        }
    }
}

private struct StackHarnessChip: View {
    @Environment(\.colorScheme) private var colorScheme

    let harness: StackHarness

    var body: some View {
        HStack(spacing: 6) {
            RoundedRectangle(cornerRadius: IterRadius.harnessSwatch)
                .fill(tintColor)
                .frame(width: 7, height: 7)
                .accessibilityHidden(true)

            Text(verbatim: harness.shortCode)
                .font(IterFont.monoLabel)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
        }
        .padding(.horizontal, 8)
        .frame(height: 26)
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.pill))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.pill)
                .stroke(
                    harness.source == .detected ? Color.iterBorder(for: colorScheme) : tintColor,
                    style: StrokeStyle(lineWidth: 1, dash: harness.source == .detected ? [3, 3] : [])
                )
        }
        .accessibilityLabel("Harness \(harness.shortCode)")
    }

    private var tintColor: Color {
        harness.harnessID?.tint.color ?? Color.iterTextTertiary(for: colorScheme)
    }
}

private struct StackEditableListSection<Content: View>: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let emptyTitle: String
    let addButtonTitle: String
    let addAction: () -> Void
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            HStack {
                StackSectionTitle(title: title, detail: nil)
                Spacer()
                IterButton(title: addButtonTitle, action: addAction)
            }

            content
        }
    }
}

private struct StackSkillRow: View {
    @Environment(\.colorScheme) private var colorScheme
    @Binding var skill: StackSkill
    let removeAction: () -> Void

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            TextField("Skill", text: $skill.name)
                .textFieldStyle(.plain)
                .font(IterFont.sansBody)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .frame(minWidth: 120)

            TextField("Source path", text: $skill.sourcePath)
                .textFieldStyle(.plain)
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))

            RemoveIconButton(action: removeAction)
        }
        .stackRowStyle()
    }
}

private struct StackSkillEntryRow: View {
    @Environment(\.colorScheme) private var colorScheme
    @Binding var name: String
    @Binding var sourcePath: String
    let addAction: () -> Void

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            TextField("Skill", text: $name)
                .textFieldStyle(.plain)
                .font(IterFont.sansBody)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .frame(minWidth: 120)
                .onSubmit(addAction)

            TextField("Source path", text: $sourcePath)
                .textFieldStyle(.plain)
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                .onSubmit(addAction)

            AddIconButton(action: addAction)
        }
        .stackRowStyle()
    }
}

private struct StackDocRow: View {
    @Environment(\.colorScheme) private var colorScheme
    @Binding var reference: StackDocReference
    let removeAction: () -> Void

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            TextField("URL or repo path", text: $reference.value)
                .textFieldStyle(.plain)
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

            RemoveIconButton(action: removeAction)
        }
        .stackRowStyle()
    }
}

private struct StackDocEntryRow: View {
    @Environment(\.colorScheme) private var colorScheme
    @Binding var reference: String
    let addAction: () -> Void

    var body: some View {
        HStack(spacing: IterSpacing.gapSmall) {
            TextField("URL or repo path", text: $reference)
                .textFieldStyle(.plain)
                .font(IterFont.monoSmall)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .onSubmit(addAction)

            AddIconButton(action: addAction)
        }
        .stackRowStyle()
    }
}

private struct StackNotesSection: View {
    @Environment(\.colorScheme) private var colorScheme
    @Binding var notes: String

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapSmall) {
            StackSectionTitle(title: "Notes", detail: nil)

            TextEditor(text: $notes)
                .font(IterFont.sans(size: 13))
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                .scrollContentBackground(.hidden)
                .frame(minHeight: 120)
                .padding(8)
                .background(Color.iterPanel(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.card))
                .overlay {
                    RoundedRectangle(cornerRadius: IterRadius.card)
                        .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
                }
        }
    }
}

private struct StackMeShareSheet: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.colorScheme) private var colorScheme
    @Bindable var store: StackStore

    @State private var selectedDocs = Set<String>()

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            HStack {
                Text(verbatim: "Add share")
                    .font(IterFont.sansKPIValue)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))

                Spacer()

                Button {
                    dismiss()
                } label: {
                    Image(systemName: "xmark")
                        .frame(width: 24, height: 24)
                }
                .buttonStyle(.plain)
                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            }

            StackDocSelectionList(docs: store.stack.fileReferenceValues, selection: $selectedDocs)

            Button {
                Task {
                    await store.share(target: .team, includedDocs: Array(selectedDocs))
                    dismiss()
                }
            } label: {
                HStack {
                    Image(systemName: "person.3")
                    Text(verbatim: "Whole team")
                    Spacer()
                }
                .stackShareOptionStyle()
            }
            .buttonStyle(.plain)

            VStack(alignment: .leading, spacing: 6) {
                Text(verbatim: "Specific teammate")
                    .font(IterFont.sansSectionTitle)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))

                ForEach(store.teamMembers) { member in
                    Button {
                        Task {
                            await store.share(target: .user(member), includedDocs: Array(selectedDocs))
                            dismiss()
                        }
                    } label: {
                        HStack(spacing: IterSpacing.gapSmall) {
                            Avatar(initials: member.initials, seed: member.avatarSeed)
                            Text(verbatim: member.displayName)
                                .font(IterFont.sansBody)
                            Spacer()
                        }
                        .stackShareOptionStyle()
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .padding(IterSpacing.gapLarge)
        .frame(width: 420)
        .background(Color.iterPanel(for: colorScheme))
        .onAppear {
            selectedDocs = Set(store.stack.fileReferenceValues)
        }
    }
}

private struct StackDocSelectionList: View {
    @Environment(\.colorScheme) private var colorScheme

    let docs: [String]
    @Binding var selection: Set<String>

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            StackSectionTitle(title: "File references", detail: "\(selection.count)/\(docs.count)")

            if docs.isEmpty {
                Text(verbatim: "No file references")
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.vertical, 8)
            } else {
                ForEach(docs, id: \.self) { doc in
                    Toggle(isOn: Binding(
                        get: { selection.contains(doc) },
                        set: { isSelected in
                            if isSelected {
                                selection.insert(doc)
                            } else {
                                selection.remove(doc)
                            }
                        }
                    )) {
                        Text(verbatim: doc)
                            .font(IterFont.monoSmall)
                            .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
                            .lineLimit(1)
                    }
                    .toggleStyle(.checkbox)
                }
            }
        }
    }
}

struct StackRightRailView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Bindable var store: StackStore
    var onSimulate: (UUID) -> Void = { _ in }

    var body: some View {
        VStack(alignment: .leading, spacing: IterSpacing.gapMedium) {
            RailCard(
                title: "Share grants",
                count: "\(store.stack.shareGrants.count)",
                items: []
            )
            StackShareGrantList(store: store)
            StackSharedWithMeCard(items: store.sharedWithMe, onSimulate: onSimulate)
            Spacer()
        }
        .padding(IterSpacing.gapMedium)
        .frame(maxHeight: .infinity, alignment: .topLeading)
        .background(Color.iterRail(for: colorScheme))
        .overlay(alignment: .leading) {
            DividerLine(axis: .vertical)
        }
    }
}

private struct StackShareGrantList: View {
    @Environment(\.colorScheme) private var colorScheme
    @Bindable var store: StackStore

    var body: some View {
        VStack(spacing: 0) {
            if store.stack.shareGrants.isEmpty {
                Text(verbatim: "No share grants")
                    .font(IterFont.monoLabel)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(12)
            } else {
                ForEach(store.stack.shareGrants) { grant in
                    HStack(spacing: IterSpacing.gapSmall) {
                        Avatar(initials: grant.initials, seed: grant.avatarSeed)
                        Text(verbatim: grant.displayName)
                            .font(IterFont.sansSmall)
                            .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                            .lineLimit(1)
                        Spacer()
                        Button {
                            Task { await store.revoke(grant) }
                        } label: {
                            Image(systemName: "xmark.circle")
                                .frame(width: 22, height: 22)
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                    }
                    .padding(12)
                    .overlay(alignment: .top) {
                        DividerLine()
                    }
                }
            }
        }
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.card)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }
}

private struct StackSharedWithMeCard: View {
    @Environment(\.colorScheme) private var colorScheme

    let items: [SharedStackSummary]
    let onSimulate: (UUID) -> Void

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text(verbatim: "Shared with me")
                    .font(IterFont.sansSectionTitle)
                    .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                Spacer()
                Text(verbatim: "\(items.count)")
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            }
            .padding(.horizontal, 12)
            .padding(.top, 10)
            .padding(.bottom, 6)

            ForEach(items) { item in
                Button {
                    onSimulate(item.userID)
                } label: {
                    HStack(spacing: IterSpacing.gapSmall) {
                        Avatar(initials: item.initials, seed: item.avatarSeed)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(verbatim: item.displayName)
                                .font(IterFont.sansSmall)
                                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
                            Text(verbatim: item.stackName)
                                .font(IterFont.monoSmall)
                                .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                                .lineLimit(1)
                        }
                        Spacer()
                        Image(systemName: "arrow.up.forward")
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
                            .accessibilityHidden(true)
                    }
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Try \(item.displayName)'s stack")
                .padding(.horizontal, 12)
                .padding(.vertical, 8)
                .overlay(alignment: .top) {
                    DividerLine()
                }
            }
        }
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.card))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.card)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
    }
}

private struct StackSectionTitle: View {
    @Environment(\.colorScheme) private var colorScheme

    let title: String
    let detail: String?

    var body: some View {
        HStack(spacing: 6) {
            Text(verbatim: title)
                .font(IterFont.sansSectionTitle)
                .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
            if let detail {
                Text(verbatim: detail)
                    .font(IterFont.monoSmall)
                    .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
            }
        }
    }
}

private struct RemoveIconButton: View {
    @Environment(\.colorScheme) private var colorScheme
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: "minus.circle")
                .frame(width: 22, height: 22)
        }
        .buttonStyle(.plain)
        .foregroundStyle(Color.iterTextTertiary(for: colorScheme))
    }
}

private struct AddIconButton: View {
    @Environment(\.colorScheme) private var colorScheme
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: "plus.circle")
                .frame(width: 22, height: 22)
        }
        .buttonStyle(.plain)
        .foregroundStyle(Color.iterAccent(for: colorScheme))
    }
}

private struct StackToastView: View {
    @Environment(\.colorScheme) private var colorScheme
    let toast: StackToast?

    var body: some View {
        if let toast {
            Text(verbatim: toast.message)
                .font(IterFont.monoLabel)
                .foregroundStyle(
                    toast.kind == .warning ? Color.iterBad(for: colorScheme) : Color.iterTextPrimary(for: colorScheme)
                )
                .padding(.horizontal, 10)
                .frame(height: 28)
                .background(Color.iterPanel(for: colorScheme))
                .clipShape(.rect(cornerRadius: IterRadius.pill))
                .overlay {
                    RoundedRectangle(cornerRadius: IterRadius.pill)
                        .stroke(borderColor(for: toast.kind))
                }
        }
    }

    private func borderColor(for kind: StackToastKind) -> Color {
        kind == .warning ? Color.iterBad(for: colorScheme) : Color.iterBorder(for: colorScheme)
    }
}

private struct StackRowModifier: ViewModifier {
    @Environment(\.colorScheme) private var colorScheme

    func body(content: Content) -> some View {
        content
            .padding(.horizontal, 10)
            .frame(minHeight: 34)
            .background(Color.iterPanel(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.card))
            .overlay {
                RoundedRectangle(cornerRadius: IterRadius.card)
                    .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
            }
    }
}

private struct StackShareOptionModifier: ViewModifier {
    @Environment(\.colorScheme) private var colorScheme

    func body(content: Content) -> some View {
        content
            .font(IterFont.sansBody)
            .foregroundStyle(Color.iterTextPrimary(for: colorScheme))
            .padding(.horizontal, 10)
            .frame(height: 34)
            .background(Color.iterSelected(for: colorScheme))
            .clipShape(.rect(cornerRadius: IterRadius.card))
    }
}

private extension View {
    func stackRowStyle() -> some View {
        modifier(StackRowModifier())
    }

    func stackShareOptionStyle() -> some View {
        modifier(StackShareOptionModifier())
    }
}

private struct StatusPillView: View {
    @Environment(\.colorScheme) private var colorScheme

    let label: String

    var body: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(Color.iterGood(for: colorScheme))
                .frame(width: 6, height: 6)
                .accessibilityHidden(true)

            Text(verbatim: label)
                .font(IterFont.monoLabel)
                .foregroundStyle(Color.iterTextSecondary(for: colorScheme))
        }
        .padding(.horizontal, 8)
        .frame(height: 26)
        .background(Color.iterPanel(for: colorScheme))
        .clipShape(.rect(cornerRadius: IterRadius.pill))
        .overlay {
            RoundedRectangle(cornerRadius: IterRadius.pill)
                .stroke(Color.iterBorder(for: colorScheme), lineWidth: 1)
        }
        .accessibilityElement(children: .combine)
    }
}

private struct DividerLine: View {
    @Environment(\.colorScheme) private var colorScheme

    enum Axis {
        case horizontal
        case vertical
    }

    var axis: Axis = .horizontal

    var body: some View {
        Rectangle()
            .fill(Color.iterBorder(for: colorScheme))
            .frame(
                width: axis == .vertical ? 1 : nil,
                height: axis == .horizontal ? 1 : nil
            )
    }
}

#Preview("Stack Me") {
    HStack(spacing: 0) {
        StackMeView(store: StackStore())
        StackRightRailView(store: StackStore())
            .frame(width: IterSpacing.railWidthMe)
    }
    .environment(ThemeStore())
    .environment(DaemonClient())
    .preferredColorScheme(.light)
}
