import SwiftUI

enum IterSpacing {
    static let rowHeight: CGFloat = 30
    static let titlebarHeight: CGFloat = 38
    static let subbarHeight: CGFloat = 42
    static let sidebarWidth: CGFloat = 224
    static let railWidthMe: CGFloat = 296
    static let railWidthTeam: CGFloat = 296
    static let railWidthSession: CGFloat = 320
    static let cardGridMin: CGFloat = 308
    static let cardPaddingTop: CGFloat = 12
    static let cardPaddingHorizontal: CGFloat = 12
    static let cardPaddingBottom: CGFloat = 10
    static let sectionGap: CGFloat = 18
    static let mainPanePaddingTop: CGFloat = 18
    static let mainPanePaddingHorizontal: CGFloat = 22
    static let mainPanePaddingBottom: CGFloat = 28
    static let gapTiny: CGFloat = 4
    static let gapSmall: CGFloat = 8
    static let gapMedium: CGFloat = 12
    static let gapLarge: CGFloat = 18
    static let windowMaxWidth: CGFloat = 1480
    static let windowMaxHeight: CGFloat = 940

    static var cardPadding: EdgeInsets {
        EdgeInsets(
            top: cardPaddingTop,
            leading: cardPaddingHorizontal,
            bottom: cardPaddingBottom,
            trailing: cardPaddingHorizontal
        )
    }

    static var mainPanePadding: EdgeInsets {
        EdgeInsets(
            top: mainPanePaddingTop,
            leading: mainPanePaddingHorizontal,
            bottom: mainPanePaddingBottom,
            trailing: mainPanePaddingHorizontal
        )
    }
}
