package types

type FolderAccess struct {
	FolderPath string
	Emails     map[string]bool // untuk hindari duplikat
}

type UserFolderAccess struct {
	Email    string
	FolderId string
}
