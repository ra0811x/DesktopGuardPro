package maintenance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/storage"
	platformwindows "desktopguardpro/internal/windows"
)

func checkPersistedProtectionState(dataDirectory string) error {
	if err := platformwindows.ValidateMaintenanceDataDirectory(dataDirectory); err != nil {
		return fmt.Errorf("validate persisted protection state permissions: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, found, err := storage.ReadPersistedProtectionSession(ctx, filepath.Join(dataDirectory, "desktop-guard.db"))
	if err != nil {
		return fmt.Errorf("read persisted protection state: %w", err)
	}
	if !found {
		return nil
	}
	if session.State == domain.SessionStateFinalizing {
		if err := completeInterruptedFinalization(ctx, dataDirectory, session.ID); err != nil {
			return fmt.Errorf("complete interrupted protection finalization: %w", err)
		}
		return nil
	}
	return fmt.Errorf("%w: persisted state=%s; restore the service and finish this session before maintenance", ErrActiveProtectionSession, session.State)
}

func completeInterruptedFinalization(ctx context.Context, dataDirectory, sessionID string) error {
	keyPath := filepath.Join(dataDirectory, "storage.key")
	info, err := os.Lstat(keyPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid persisted storage key")
	}
	keyStore, err := storage.NewDataKeyStore(storage.NewDPAPIKeyProtector())
	if err != nil {
		return err
	}
	masterKey, err := keyStore.LoadOrCreate(keyPath)
	if err != nil {
		return err
	}
	defer clear(masterKey)
	payloadCipher, err := storage.NewPayloadCipher(masterKey)
	if err != nil {
		return err
	}
	eventIntegrity, err := storage.NewEventIntegrity(masterKey)
	if err != nil {
		return err
	}
	database, err := storage.Open(ctx, filepath.Join(dataDirectory, "desktop-guard.db"))
	if err != nil {
		return err
	}
	defer database.Close()
	repository, err := storage.NewRepository(database, payloadCipher, eventIntegrity)
	if err != nil {
		return err
	}
	session, err := repository.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.State != domain.SessionStateFinalizing {
		return fmt.Errorf("%w: persisted state changed to %s", ErrActiveProtectionSession, session.State)
	}
	previousState := session.State
	if err := session.Transition(domain.SessionStateCompleted); err != nil {
		return err
	}
	observedUTC := time.Now().UTC()
	payload, err := json.Marshal(struct {
		PreviousState  domain.SessionState `json:"previousState"`
		CurrentState   domain.SessionState `json:"currentState"`
		ObservedUTC    time.Time           `json:"observedUtc"`
		RecoveryReason string              `json:"recoveryReason"`
	}{
		PreviousState: previousState, CurrentState: session.State, ObservedUTC: observedUTC,
		RecoveryReason: "service_stopped_during_finalization",
	})
	if err != nil {
		return err
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	_, err = repository.UpdateSessionAndAppendEvent(ctx, session, observedUTC, domain.AuditEvent{
		EventID: "maintenance-recovery-" + hex.EncodeToString(random), SessionID: session.ID,
		Category: domain.EventCategoryHealth, Action: "protection_ended", Severity: domain.EventSeverityLow,
		ObservedUTC: observedUTC, MonotonicTicks: observedUTC.UnixNano(),
		Source: "desktop_guard_maintenance", Confidence: domain.EventConfidenceDirect,
	}, payload)
	return err
}
