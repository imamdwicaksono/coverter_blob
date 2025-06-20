package types

type FolderAccess struct {
	FolderPath string
	Emails     map[string]bool // untuk hindari duplikat
}

type EmailAccess struct {
	Email          string            `json:"email"`
	FolderRole     string            `json:"role"`
	SharepointRole *FolderRoleAccess `json:"sharepoint_role"` // Menyimpan role SharePoint
}

type UserFolderAccess struct {
	EmailAccess EmailAccess
	FolderId    string
	FolderPath  string // Menyimpan path folder
}

type FolderRoleAccess struct {
	FolderRole     string `json:"folder_role"`
	RolePermission string `json:"role_permission"`
}
