import SwiftUI

enum IterFont {
    static let sansFamily = "IBM Plex Sans"
    static let monoFamily = "IBM Plex Mono"

    static let sansBody = sans(size: 13)
    static let sansLabel = sans(size: 11)
    static let sansSmall = sans(size: 10.5)
    static let sansSectionTitle = sans(size: 11.5, weight: .semibold)
    static let sansCardTitle = sans(size: 13, weight: .medium)
    static let sansKPIValue = sans(size: 22, weight: .medium)
    static let sansKPIUnit = sans(size: 12)

    static let monoBody = mono(size: 13)
    static let monoTitle = mono(size: 12.5, weight: .medium)
    static let monoLabel = mono(size: 11)
    static let monoSmall = mono(size: 10.5)
    static let monoTiny = mono(size: 10)
    static let monoScore = mono(size: 11.5, weight: .medium)
    static let monoOutcomeValue = mono(size: 15)
    static let monoAvatar = mono(size: 9.5, weight: .semibold)

    static func sans(size: CGFloat, weight: Font.Weight = .regular) -> Font {
        .custom(sansFamily, size: size)
            .weight(weight)
    }

    static func mono(size: CGFloat, weight: Font.Weight = .regular) -> Font {
        .custom(monoFamily, size: size)
            .weight(weight)
            .monospacedDigit()
    }
}
