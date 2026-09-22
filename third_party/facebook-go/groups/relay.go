package groups

// relayProviderVars returns the standard Relay internal provider variables
// that Facebook requires for most modern Comet GraphQL queries.
// These are extracted from the preloaded page data and are stable across sessions.
func relayProviderVars() map[string]interface{} {
	return map[string]interface{}{
		"__relay_internal__pv__CometFeedStory_enable_post_permalink_white_space_clickrelayprovider": false,
		"__relay_internal__pv__CometImmersivePhotoCanUserDisable3DMotionrelayprovider":              false,
		"__relay_internal__pv__CometUFICommentActionLinksRewriteEnabledrelayprovider":               false,
		"__relay_internal__pv__CometUFICommentAutoTranslationTyperelayprovider":                     "ORIGINAL",
		"__relay_internal__pv__CometUFICommentAvatarStickerAnimatedImagerelayprovider":              false,
		"__relay_internal__pv__CometUFIReactionsEnableShortNamerelayprovider":                       false,
		"__relay_internal__pv__CometUFIShareActionMigrationrelayprovider":                           true,
		"__relay_internal__pv__CometUFISingleLineUFIrelayprovider":                                  false,
		"__relay_internal__pv__CometUFI_dedicated_comment_routable_dialog_gkrelayprovider":          true,
		"__relay_internal__pv__FBReelsIFUTileContent_reelsIFUPlayOnHoverrelayprovider":              true,
		"__relay_internal__pv__FBReelsMediaFooter_comet_enable_reels_ads_gkrelayprovider":           true,
		"__relay_internal__pv__FBReels_deprecate_short_form_video_context_gkrelayprovider":          true,
		"__relay_internal__pv__FBReels_enable_view_dubbed_audio_type_gkrelayprovider":               true,
		"__relay_internal__pv__GHLShouldChangeAdIdFieldNamerelayprovider":                           true,
		"__relay_internal__pv__GHLShouldChangeSponsoredDataFieldNamerelayprovider":                  true,
		"__relay_internal__pv__GroupsCometGYSJFeedItemHeightrelayprovider":                          206,
		"__relay_internal__pv__IsMergQAPollsrelayprovider":                                          false,
		"__relay_internal__pv__IsWorkUserrelayprovider":                                             false,
		"__relay_internal__pv__ShouldEnableBakedInTextStoriesrelayprovider":                         true,
		"__relay_internal__pv__StoriesShouldIncludeFbNotesrelayprovider":                            false,
		"__relay_internal__pv__TestPilotShouldIncludeDemoAdUseCaserelayprovider":                    false,
		"__relay_internal__pv__WorkCometIsEmployeeGKProviderrelayprovider":                          false,
	}
}

// mergeVars combines query-specific variables with the standard relay provider variables.
func mergeVars(specific map[string]interface{}) map[string]interface{} {
	merged := relayProviderVars()
	for k, v := range specific {
		merged[k] = v
	}
	return merged
}
