// Copyright (C)  2026 Graylog, Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the Server Side Public License, version 1,
// as published by MongoDB, Inc.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// Server Side Public License for more details.
//
// You should have received a copy of the Server Side Public License
// along with this program. If not, see
// <http://www.mongodb.com/licensing/server-side-public-license>.
//
// SPDX-License-Identifier: SSPL-1.0

package persistence

import (
	"fmt"

	"github.com/Graylog2/collector/superv/identity"
	"go.uber.org/zap"
)

// InitIdentity ensures that the instance UID, the signing key, and the encryption key are created and persisted.
func InitIdentity(logger *zap.Logger, persistenceDir string, keysDir string) error {
	instanceDataExists, err := InstanceDataExists(persistenceDir)
	if err != nil {
		return err
	}

	signingKeyExists, err := SigningKeyExists(keysDir)
	if err != nil {
		return err
	}
	encryptionKeyExists, err := EncryptionKeyExists(keysDir)
	if err != nil {
		return err
	}
	certificateExists, err := CertificateExists(keysDir)
	if err != nil {
		return err
	}

	if certificateExists && (!instanceDataExists || !signingKeyExists || !encryptionKeyExists) {
		return fmt.Errorf("certificate exists but identity keys are incomplete - remove certificate in %s", keysDir)
	}

	if !instanceDataExists {
		// A new identity requires new signing and encryption keys as well to avoid reusing
		// existing keys with a new identity.
		if signingKeyExists || encryptionKeyExists {
			return fmt.Errorf("signing and encryption keys exist but no identity data")
		}
		data := identity.CreateInstanceData()
		logger.Debug("Generating instance data", zap.String("instance_uid", data.InstanceUID))

		if err := SaveInstanceData(persistenceDir, data); err != nil {
			return fmt.Errorf("failed to save instance data: %w", err)
		}
	}

	if !signingKeyExists {
		logger.Debug("Generating signing key")
		_, signingPriv, err := identity.GenerateSigningKeypair()
		if err != nil {
			return fmt.Errorf("failed to generate signing keypair: %w", err)
		}

		logger.Debug("Saving signing key")
		if err := SaveSigningKey(keysDir, signingPriv); err != nil {
			return fmt.Errorf("failed to save signing key: %w", err)
		}
	}

	if !encryptionKeyExists {
		logger.Debug("Generating encryption key")
		_, encPriv, err := identity.GenerateEncryptionKeypair()
		if err != nil {
			return fmt.Errorf("failed to generate encryption keypair: %w", err)
		}

		logger.Debug("Saving encryption key")
		if err := SaveEncryptionKey(keysDir, encPriv); err != nil {
			return fmt.Errorf("failed to save encryption key: %w", err)
		}
	}

	return nil
}
