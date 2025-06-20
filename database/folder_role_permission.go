package database

import (
	"database/sql"
	"fmt"
)

func GetFolderRolePermission(db *sql.DB) (*sql.Rows, error) {
	sql := `SELECT * FROM teradocu.folder_role_permission frp`

	rows, err := db.Query(sql)
	if err != nil {
		fmt.Println("❌ Gagal menjalankan query:", err)
		return nil, err
	}

	return rows, nil
}
