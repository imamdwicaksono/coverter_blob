package database

import (
	"database/sql"
	"fmt"
)

func GetUserByFolderId(folderId string, db *sql.DB) (*sql.Rows, error) {
	sql := `WITH RECURSIVE item_hierarchy AS (
		SELECT
			id,
			name,
			owner,
			fullpath AS file_path,
			parent_id,
			id AS root_id,
			name AS root_name,
			0 AS level
		FROM teradocu.folder
		WHERE parent_id IS NULL
		UNION ALL
		SELECT
			f.id,
			f.name,
			f.owner,
			f.fullpath AS file_path,
			f.parent_id,
			ih.root_id,
			ih.root_name,
			ih.level + 1
		FROM teradocu.folder f
		JOIN item_hierarchy ih ON f.parent_id = ih.id
	)
	SELECT
		pr.email,
		fpr.profile_id,
		ih2.id AS folder_id,
		ih2.file_path
	FROM item_hierarchy ih2
	JOIN teradocu.folder_profile_role fpr ON fpr.folder_id = ih2.id
	JOIN teradocu.user_profile up on fpr.profile_id = up.profile_id
	JOIN teradocu.employee_user eu1 ON up.user_id = eu1.id
	JOIN teradocu.person pr ON pr.id = eu1.person_id
	WHERE ih2.id = $1 AND eu1.active = true
	GROUP BY ih2.id, pr.email, fpr.profile_id, ih2.file_path`

	rows, err := db.Query(sql, folderId)
	if err != nil {
		fmt.Println("❌ Gagal menjalankan query:", err)
		return nil, err
	}

	return rows, nil
}

func GetUserByProfile(db *sql.DB, profileID string) (*sql.Rows, error) {
	sql := `WITH RECURSIVE item_hierarchy AS (
		SELECT
			id,
			name,
			owner,
			fullpath AS file_path,
			parent_id,
			id AS root_id,
			name AS root_name,
			0 AS level
		FROM teradocu.folder
		WHERE parent_id IS NULL
		UNION ALL
		SELECT
			f.id,
			f.name,
			f.owner,
			f.fullpath AS file_path,
			f.parent_id,
			ih.root_id,
			ih.root_name,
			ih.level + 1
		FROM teradocu.folder f
		JOIN item_hierarchy ih ON f.parent_id = ih.id
	)
	SELECT
		pr.email,
		fpr.profile_id,
		ih2.id AS folder_id,
		ih2.file_path,
		fpr.folder_role

	FROM item_hierarchy ih2
	JOIN teradocu.folder_profile_role fpr ON fpr.folder_id = ih2.id
	JOIN teradocu.user_profile up on fpr.profile_id = up.profile_id
	JOIN teradocu.employee_user eu1 ON up.user_id = eu1.id
	JOIN teradocu.person pr ON pr.id = eu1.person_id
 	WHERE up.profile_id = $1 AND eu1.active = true
	GROUP BY ih2.id, pr.email, fpr.profile_id, ih2.file_path, fpr.folder_role`

	rows, err := db.Query(sql, profileID)
	if err != nil {
		fmt.Println("❌ Gagal menjalankan query:", err)
		return nil, err
	}

	return rows, nil
}

func GetUserByEmail(db *sql.DB, email string) (*sql.Rows, error) {
	sql := `WITH RECURSIVE item_hierarchy AS (
		SELECT
			id,
			name,
			owner,
			fullpath AS file_path,
			parent_id,
			id AS root_id,
			name AS root_name,
			0 AS level
		FROM teradocu.folder
		WHERE parent_id IS NULL
		UNION ALL
		SELECT
			f.id,
			f.name,
			f.owner,
			f.fullpath AS file_path,
			f.parent_id,
			ih.root_id,
			ih.root_name,
			ih.level + 1
		FROM teradocu.folder f
		JOIN item_hierarchy ih ON f.parent_id = ih.id
	)
	SELECT
		pr.email,
		fpr.profile_id,
		ih2.id AS folder_id,
		ih2.file_path
	FROM item_hierarchy ih2
	JOIN teradocu.folder_profile_role fpr ON fpr.folder_id = ih2.id
	JOIN teradocu.user_profile up on fpr.profile_id = up.profile_id
	JOIN teradocu.employee_user eu1 ON up.user_id = eu1.id
	JOIN teradocu.person pr ON pr.id = eu1.person_id
	WHERE pr.email = '$1' AND eu1.active = true
	GROUP BY ih2.id, pr.email, fpr.profile_id, ih2.file_path`

	rows, err := db.Query(sql, email)
	if err != nil {
		fmt.Println("❌ Gagal menjalankan query:", err)
		return nil, err
	}

	return rows, nil
}

func GetUserByID(db *sql.DB, userID string) (*sql.Rows, error) {
	sql := `WITH RECURSIVE item_hierarchy AS (
		SELECT
			id,
			name,
			owner,
			fullpath AS file_path,
			parent_id,
			id AS root_id,
			name AS root_name,
			0 AS level
		FROM teradocu.folder
		WHERE parent_id IS NULL
		UNION ALL
		SELECT
			f.id,
			f.name,
			f.owner,
			f.fullpath AS file_path,
			f.parent_id,
			ih.root_id,
			ih.root_name,
			ih.level + 1
		FROM teradocu.folder f
		JOIN item_hierarchy ih ON f.parent_id = ih.id
	)
	SELECT
		pr.email,
		fpr.profile_id,
		ih2.id AS folder_id,
		ih2.file_path
	FROM item_hierarchy ih2
	JOIN teradocu.folder_profile_role fpr ON fpr.folder_id = ih2.id
	JOIN teradocu.user_profile up on fpr.profile_id = up.profile_id
	JOIN teradocu.employee_user eu1 ON up.user_id = eu1.id
	JOIN teradocu.person pr ON pr.id = eu1.person_id
	WHERE eu1.id = '$1' AND eu1.active = true
	GROUP BY ih2.id, pr.email, fpr.profile_id, ih2.file_path`

	rows, err := db.Query(sql, userID)
	if err != nil {
		fmt.Println("❌ Gagal menjalankan query:", err)
		return nil, err
	}

	return rows, nil
}
