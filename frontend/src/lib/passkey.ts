import {
  browserSupportsWebAuthn,
  startAuthentication,
  startRegistration,
  type AuthenticationResponseJSON,
  type PublicKeyCredentialCreationOptionsJSON,
  type PublicKeyCredentialRequestOptionsJSON,
  type RegistrationResponseJSON,
} from '@simplewebauthn/browser'
import { api } from './api'

export function supportsPasskeys() {
  return browserSupportsWebAuthn()
}

export async function createPasskey() {
  const { ceremonyId, options } = await api.beginPasskeyRegistration()
  const response = await startRegistration({ optionsJSON: options as PublicKeyCredentialCreationOptionsJSON })
  await api.finishPasskeyRegistration(ceremonyId, response as RegistrationResponseJSON)
}

export async function getPasskeyAssertion() {
  const { ceremonyId, options } = await api.beginPasskeyLogin()
  const response = await startAuthentication({ optionsJSON: options as PublicKeyCredentialRequestOptionsJSON })
  await api.finishPasskeyLogin(ceremonyId, response as AuthenticationResponseJSON)
}
